package sandbox_test

// Temporary #55 CI diagnosis (DEBUG-55). Remove this file before the branch ships.
//
// It runs the production sandbox.WaitReady with the host's real ssh against the protocol test's own
// server (serveTestSSHKeyExchange, unchanged), and only observes: each ssh attempt's timing, exit,
// run error, stderr lines with arrival offsets, and known_hosts state, plus the server's socket
// operations. The runner passes every request through to platform.Run unchanged and tees stderr, so
// the readiness decision is the production one. Each mode runs with the production test's 2 s
// deadline and with a longer observation deadline that only shows whether ssh is slow or stuck; the
// valid exchange with the 2 s deadline is this process's first ssh start, and the ssh -V timings come
// last. The test fails if the valid exchange is not accepted within 2 s, or if either invalid exchange
// is accepted with any deadline. Output is redacted: no user paths, fingerprints, key blobs, or ports.
//
// debug55OriginalObserver, run, result, and debug55ServerConn below observe the original protocol test
// (readiness_protocol_test.go) in the full suite when DEBUG_55_ORIGINAL_OBSERVE names an output file.
// Unset, they return platform.Run and the connection unchanged and record nothing.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/sshkeys"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

const debug55Prefix = "DEBUG-55"

var (
	debug55Homes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)[a-z]:[\\/]+users[\\/]+[^\\/\s'"]+`),
		regexp.MustCompile(`(?i)/[a-z]/users/[^/\s'"]+`),
		regexp.MustCompile(`/home/[^/\s'"]+`),
	}
	debug55Fingerprint = regexp.MustCompile(`SHA256:[A-Za-z0-9+/=]+`)
	debug55Blob        = regexp.MustCompile(`[A-Za-z0-9+/=]{40,}`)
)

func debug55Redact(line string, port int) string {
	for _, pattern := range debug55Homes {
		line = pattern.ReplaceAllString(line, "<user-home>")
	}
	line = debug55Fingerprint.ReplaceAllString(line, "SHA256:<redacted>")
	line = debug55Blob.ReplaceAllString(line, "<blob>")
	if port > 0 {
		line = strings.ReplaceAll(line, strconv.Itoa(port), "<port>")
	}
	if len(line) > 220 {
		line = line[:220] + "..."
	}
	return line
}

type debug55Clock struct {
	start time.Time
	mu    sync.Mutex
	lines []string
}

func (c *debug55Clock) add(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf("+%5dms ", time.Since(c.start).Milliseconds())+fmt.Sprintf(format, args...))
}

func (c *debug55Clock) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

// debug55Conn records the server's socket operations; data passes through unchanged.
type debug55Conn struct {
	net.Conn
	id    int
	clock *debug55Clock
}

func (c debug55Conn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.clock.add("server conn=%d read n=%d err=%v", c.id, n, err)
	return n, err
}

func (c debug55Conn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.clock.add("server conn=%d write n=%d err=%v", c.id, n, err)
	return n, err
}

// debug55Lines stamps each stderr line with its arrival offset.
type debug55Lines struct {
	clock   *debug55Clock
	partial string
	mu      sync.Mutex
	lines   []string
}

func (w *debug55Lines) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := w.partial + strings.ReplaceAll(string(p), "\r", "")
	parts := strings.Split(text, "\n")
	w.partial = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		w.lines = append(w.lines, fmt.Sprintf("+%5dms %s", time.Since(w.clock.start).Milliseconds(), line))
	}
	return len(p), nil
}

func (w *debug55Lines) all() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.partial != "" {
		return append(append([]string(nil), w.lines...), fmt.Sprintf("+%5dms %s (no newline)", time.Since(w.clock.start).Milliseconds(), w.partial))
	}
	return append([]string(nil), w.lines...)
}

type debug55Attempt struct {
	startMS, durationMS int64
	exit                int
	runErr, ctxErr      error
	constantArgument    bool
	privateDir          bool
	newkeys             bool
	knownHostsExists    bool
	knownHostsValid     bool
	stderr              *debug55Lines
	remainingMS         int64 // original-test observer only: time left before the deadline when ssh returned
	observerUS          int64 // original-test observer only: time the observer spent before returning to WaitReady
}

func TestDebug55ReadinessOnThisRunner(t *testing.T) {
	if os.Getenv("DEBUG_55_READINESS") != "1" {
		t.Skip("set DEBUG_55_READINESS=1 to run the #55 CI diagnosis")
	}
	observe := 20 * time.Second
	if value := os.Getenv("DEBUG_55_OBSERVE_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 3 {
			t.Fatalf("DEBUG_55_OBSERVE_SECONDS must be an integer of at least 3")
		}
		observe = time.Duration(seconds) * time.Second
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatalf("%s ssh is not on PATH", debug55Prefix)
	}
	t.Logf("%s ssh=%s", debug55Prefix, debug55Redact(sshPath, 0))
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		for _, name := range []string{"ssh.exe", "ssh"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
				t.Logf("%s path-candidate=%s", debug55Prefix, debug55Redact(filepath.Join(dir, name), 0))
			}
		}
	}
	temp := os.TempDir()
	t.Logf("%s temp-has-tilde=%t temp-has-space=%t temp-is-abs=%t", debug55Prefix, strings.Contains(temp, "~"), strings.ContainsAny(temp, " \t"), filepath.IsAbs(temp))

	validRed, invalidAccepted := false, false
	for _, mode := range []struct {
		name  string
		ready bool
	}{{"valid-exchange", true}, {"corrupted-exchange-signature", false}, {"host-key-without-signature", false}} {
		for _, deadline := range []time.Duration{2 * time.Second, observe} {
			err := debug55Run(t, mode.name, deadline)
			matches := (err == nil) == mode.ready
			t.Logf("%s summary mode=%s deadline=%s ready=%t matches-test-expectation=%t", debug55Prefix, mode.name, deadline, err == nil, matches)
			if mode.ready && deadline == 2*time.Second && err != nil {
				validRed = true
			}
			if !mode.ready && err == nil {
				invalidAccepted = true
			}
		}
	}
	// ssh -V runs only after every readiness run, so the first observed attempt is this process's first ssh start.
	for i := 1; i <= 3; i++ {
		started := time.Now()
		version, err := exec.Command(sshPath, "-V").CombinedOutput()
		t.Logf("%s ssh-V run=%d duration-ms=%d err=%v output=%q", debug55Prefix, i, time.Since(started).Milliseconds(), err, debug55Redact(strings.TrimSpace(string(version)), 0))
	}
	if invalidAccepted {
		t.Errorf("%s verdict=RED an invalid exchange was accepted", debug55Prefix)
	}
	if validRed {
		t.Errorf("%s verdict=RED the valid exchange was not accepted within the production test's 2 s deadline", debug55Prefix)
	}
	if !invalidAccepted && !validRed {
		t.Logf("%s verdict=GREEN the valid exchange was accepted within 2 s and no invalid exchange was accepted", debug55Prefix)
	}
}

func debug55Run(t *testing.T, mode string, deadline time.Duration) error {
	t.Helper()
	clock := &debug55Clock{start: time.Now()}
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var handlers sync.WaitGroup
	go func() {
		for id := 1; ; id++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			clock.add("server conn=%d accepted", id)
			handlers.Add(1)
			go func(id int, conn net.Conn) {
				defer handlers.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				serveTestSSHKeyExchange(debug55Conn{Conn: conn, id: id, clock: clock}, hostKey, mode)
				clock.add("server conn=%d handler returned, closing", id)
			}(id, conn)
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	host := "[127.0.0.1]:" + strconv.Itoa(port)
	var attempts []*debug55Attempt
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	err = sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", port, func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
			return 0, nil
		}
		attempt := &debug55Attempt{startMS: time.Since(clock.start).Milliseconds(), stderr: &debug55Lines{clock: clock}}
		attempts = append(attempts, attempt)
		for i, arg := range request.Args {
			if i > 0 && request.Args[i-1] == "-o" && arg == "UserKnownHostsFile=known_hosts" {
				attempt.constantArgument = true
			}
		}
		attempt.privateDir = strings.HasPrefix(filepath.Base(request.Dir), "sandboxed-agents-readiness-")
		original := request.Streams.Stderr
		request.Streams.Stderr = debug55Tee{original, attempt.stderr}
		clock.add("ssh attempt=%d start", len(attempts))
		started := time.Now()
		attempt.exit, attempt.runErr = platform.Run(ctx, request)
		attempt.durationMS = time.Since(started).Milliseconds()
		attempt.ctxErr = ctx.Err()
		clock.add("ssh attempt=%d returned exit=%d run-error=%v ctx=%v", len(attempts), attempt.exit, attempt.runErr, attempt.ctxErr)
		if knownHosts, ok := testutil.KnownHostsFile(request.Dir, request.Args); ok {
			if data, err := os.ReadFile(knownHosts); err == nil {
				attempt.knownHostsExists = true
				for _, line := range strings.Split(string(data), "\n") {
					fields := strings.Fields(line)
					if len(fields) >= 3 && fields[0] == host {
						if _, err := sshkeys.ParsePublicKey([]byte(strings.TrimSpace(line)[len(host):])); err == nil {
							attempt.knownHostsValid = true
						}
					}
				}
			}
		}
		for _, line := range attempt.stderr.all() {
			if strings.HasSuffix(line, " "+testutil.SSHNewKeysReceived) {
				attempt.newkeys = true
			}
		}
		return attempt.exit, attempt.runErr
	})
	listener.Close()
	handlers.Wait()
	label := fmt.Sprintf("%s mode=%s deadline=%s", debug55Prefix, mode, deadline)
	if err != nil {
		t.Logf("%s wait-error=%q", label, debug55Redact(strings.ReplaceAll(err.Error(), "\n", " | "), port))
	} else {
		t.Logf("%s wait-error=none", label)
	}
	for i, a := range attempts {
		t.Logf("%s attempt=%d start-ms=%d duration-ms=%d exit=%d run-error=%v ctx-at-return=%v constant-known-hosts-argument=%t private-dir=%t newkeys=%t known-hosts-exists=%t known-hosts-valid-ed25519=%t stderr-lines=%d",
			label, i+1, a.startMS, a.durationMS, a.exit, a.runErr, a.ctxErr, a.constantArgument, a.privateDir, a.newkeys, a.knownHostsExists, a.knownHostsValid, len(a.stderr.all()))
	}
	shown := map[int]bool{}
	for _, index := range []int{0, len(attempts) - 1} {
		if index < 0 || shown[index] {
			continue
		}
		shown[index] = true
		lines := attempts[index].stderr.all()
		for i, line := range lines {
			if i == 80 {
				t.Logf("%s attempt=%d stderr ... %d more lines", label, index+1, len(lines)-80)
				break
			}
			t.Logf("%s attempt=%d stderr %s", label, index+1, debug55Redact(line, port))
		}
	}
	events := clock.snapshot()
	for i, line := range events {
		if i == 80 {
			t.Logf("%s events ... %d more", label, len(events)-80)
			break
		}
		t.Logf("%s event %s", label, debug55Redact(line, port))
	}
	return err
}

// debug55Tee passes every byte to the production stderr writer and also to the recorder.
type debug55Tee struct {
	production io.Writer
	recorder   *debug55Lines
}

func (w debug55Tee) Write(p []byte) (int, error) {
	w.recorder.Write(p)
	return w.production.Write(p)
}

var debug55ProcessStart = time.Now()

// debug55Originals maps an observed subtest of the original protocol test to its observer.
var debug55Originals sync.Map

type debug55Original struct {
	t        *testing.T
	clock    *debug55Clock
	mode     string
	ready    bool
	wall     time.Time
	port     int
	deadline time.Time
	attempts []*debug55Attempt
	waitErr  error
	done     bool
}

func debug55OriginalObserver(t *testing.T, mode string, ready bool) *debug55Original {
	path := os.Getenv("DEBUG_55_ORIGINAL_OBSERVE")
	if path == "" {
		return nil
	}
	o := &debug55Original{t: t, clock: &debug55Clock{start: time.Now()}, mode: mode, ready: ready, wall: time.Now().UTC()}
	debug55Originals.Store(t, o)
	t.Cleanup(func() {
		debug55Originals.Delete(t)
		o.write(path)
	})
	return o
}

// debug55ServerConn records the original test server's socket operations for an observed subtest.
func debug55ServerConn(t *testing.T, conn net.Conn) net.Conn {
	value, ok := debug55Originals.Load(t)
	if !ok {
		return conn
	}
	o := value.(*debug55Original)
	o.clock.mu.Lock()
	id := 1
	for _, line := range o.clock.lines {
		if strings.Contains(line, " accepted") {
			id++
		}
	}
	o.clock.mu.Unlock()
	o.clock.add("server conn=%d accepted", id)
	return debug55Conn{Conn: conn, id: id, clock: o.clock}
}

// run passes the request to next unchanged and returns its result unchanged; it only tees stderr and,
// after next returns, reads the probe's known_hosts file and times that read.
func (o *debug55Original) run(ctx context.Context, request process.Request, next process.Runner) (int, error) {
	if o == nil {
		return next(ctx, request)
	}
	attempt := &debug55Attempt{startMS: time.Since(o.clock.start).Milliseconds(), stderr: &debug55Lines{clock: o.clock}}
	o.attempts = append(o.attempts, attempt)
	for i, arg := range request.Args {
		if i > 0 && request.Args[i-1] == "-o" && arg == "UserKnownHostsFile=known_hosts" {
			attempt.constantArgument = true
		}
		if i > 0 && request.Args[i-1] == "-p" {
			o.port, _ = strconv.Atoi(arg)
		}
	}
	attempt.privateDir = strings.HasPrefix(filepath.Base(request.Dir), "sandboxed-agents-readiness-")
	if deadline, ok := ctx.Deadline(); ok {
		o.deadline = deadline
	}
	request.Streams.Stderr = debug55Tee{request.Streams.Stderr, attempt.stderr}
	o.clock.add("ssh attempt=%d start", len(o.attempts))
	started := time.Now()
	attempt.exit, attempt.runErr = next(ctx, request)
	attempt.durationMS = time.Since(started).Milliseconds()
	attempt.ctxErr = ctx.Err()
	observing := time.Now()
	if !o.deadline.IsZero() {
		attempt.remainingMS = o.deadline.Sub(observing).Milliseconds()
	}
	o.clock.add("ssh attempt=%d returned exit=%d run-error=%v ctx=%v", len(o.attempts), attempt.exit, attempt.runErr, attempt.ctxErr)
	host := "[127.0.0.1]:" + strconv.Itoa(o.port)
	if knownHosts, ok := testutil.KnownHostsFile(request.Dir, request.Args); ok {
		if data, err := os.ReadFile(knownHosts); err == nil {
			attempt.knownHostsExists = true
			for _, line := range strings.Split(string(data), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 3 && fields[0] == host {
					if _, err := sshkeys.ParsePublicKey([]byte(strings.TrimSpace(line)[len(host):])); err == nil {
						attempt.knownHostsValid = true
					}
				}
			}
		}
	}
	for _, line := range attempt.stderr.all() {
		if strings.HasSuffix(line, " "+testutil.SSHNewKeysReceived) {
			attempt.newkeys = true
		}
	}
	attempt.observerUS = time.Since(observing).Microseconds()
	return attempt.exit, attempt.runErr
}

func (o *debug55Original) result(err error) {
	if o == nil {
		return
	}
	o.waitErr, o.done = err, true
}

func (o *debug55Original) write(path string) {
	var out strings.Builder
	label := fmt.Sprintf("DEBUG-55-ORIGINAL test=%s", o.t.Name())
	line := func(format string, args ...any) {
		out.WriteString(label + " " + debug55Redact(fmt.Sprintf(format, args...), o.port) + "\n")
	}
	sshPath, _ := exec.LookPath("ssh")
	ready := o.done && o.waitErr == nil
	line("mode=%s expected-ready=%t wall-start=%s since-process-start-ms=%d ssh=%s debug-readiness-env-set=%t", o.mode, o.ready, o.wall.Format(time.RFC3339Nano), o.wall.Sub(debug55ProcessStart.UTC()).Milliseconds(), sshPath, os.Getenv("DEBUG_55_READINESS") != "")
	if o.done {
		waitErr := "none"
		if o.waitErr != nil {
			waitErr = strings.ReplaceAll(o.waitErr.Error(), "\n", " | ")
		}
		line("ready=%t matches-test-expectation=%t wait-error=%q", ready, ready == o.ready, waitErr)
	} else {
		line("wait-result=missing")
	}
	for i, a := range o.attempts {
		line("attempt=%d start-ms=%d duration-ms=%d exit=%d run-error=%v ctx-at-return=%v remaining-ms-at-return=%d observer-us=%d constant-known-hosts-argument=%t private-dir=%t newkeys=%t known-hosts-exists=%t known-hosts-valid-ed25519=%t stderr-lines=%d",
			i+1, a.startMS, a.durationMS, a.exit, a.runErr, a.ctxErr, a.remainingMS, a.observerUS, a.constantArgument, a.privateDir, a.newkeys, a.knownHostsExists, a.knownHostsValid, len(a.stderr.all()))
	}
	for i, a := range o.attempts {
		for j, text := range a.stderr.all() {
			if j == 80 {
				line("attempt=%d stderr ... %d more lines", i+1, len(a.stderr.all())-80)
				break
			}
			line("attempt=%d stderr %s", i+1, text)
		}
	}
	events := o.clock.snapshot()
	for i, event := range events {
		if i == 200 {
			line("events ... %d more", len(events)-200)
			break
		}
		line("event %s", event)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		o.t.Logf("DEBUG-55-ORIGINAL cannot write the observation file: %v", err)
		return
	}
	defer file.Close()
	file.WriteString(out.String())
}

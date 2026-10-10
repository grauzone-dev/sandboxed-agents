package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

const readinessPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea"

func TestSandboxReadinessRequiresManagerAndSSHKeyExchange(t *testing.T) {
	managerAnswered, sshAnswered := false, false
	err := sandbox.WaitReady(context.Background(), "sandboxed-agents.default.agent01", 2222, func(ctx context.Context, request process.Request) (int, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second || time.Until(deadline) <= 0 {
			t.Fatalf("probe deadline=%v present=%v", deadline, ok)
		}
		if request.Streams.Stdin != nil || request.User != nil || request.Env != nil || (request.Name == "podman") != (request.Dir == "") {
			t.Fatalf("unexpected probe settings: %+v", request)
		}
		switch request.Name {
		case "podman":
			if !slices.Equal(request.Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}) {
				t.Fatalf("manager args=%v", request.Args)
			}
			managerAnswered = true
			fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
		case "ssh":
			sshAnswered = true
			recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
		default:
			t.Fatalf("unexpected program %q", request.Name)
		}
		return 0, nil
	})
	if err != nil || !managerAnswered || !sshAnswered {
		t.Fatalf("manager=%v SSH=%v error=%v", managerAnswered, sshAnswered, err)
	}
}

func TestSandboxReadinessRejectsMalformedManagerVersion(t *testing.T) {
	for _, output := range []string{"", "0.1.0\n", "sandboxed-agents-manager \n", "sandboxed-agents-manager 0.1.0 extra\n", "sandboxed-agents-manager 0.1.0", "sandboxed-agents-manager 0.1.0\nextra\n", "sandboxed-agents-manager 0.1.0\x00\n"} {
		t.Run(fmt.Sprintf("%q", output), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					fmt.Fprint(request.Streams.Stdout, output)
				} else {
					recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
				}
				return 0, nil
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSandboxReadinessRequiresAValidSSHKeyFromItsLoopbackPort(t *testing.T) {
	for _, output := range []string{"", "# comment\n", "127.0.0.1 " + readinessPublicKey, "[127.0.0.1]:2223 " + readinessPublicKey, "[192.0.2.1]:2222 " + readinessPublicKey, "[127.0.0.1]:2222 ssh-ed25519 !!!", "[127.0.0.1]:2222 ssh-ed25519 YWJj", "[127.0.0.1]:2222 ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAINdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea", "[127.0.0.1]:2222 " + readinessPublicKey + "\fcomment"} {
		t.Run(fmt.Sprintf("%q", output), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
				} else {
					recordKnownHost(t, request, output)
				}
				return 0, nil
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSandboxReadinessRetriesUnreadyChecksAtTheReadinessInterval(t *testing.T) {
	for _, unready := range []string{"podman", "ssh"} {
		t.Run(unready, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var failedAt time.Time
			alreadyReady := false
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
				if request.Name == unready {
					if failedAt.IsZero() {
						failedAt = time.Now()
						return 1, nil
					}
					if time.Since(failedAt) < 250*time.Millisecond {
						t.Fatalf("retry after %v", time.Since(failedAt))
					}
				} else if alreadyReady {
					return 1, nil
				} else {
					alreadyReady = true
				}
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
				} else {
					recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
				}
				return 0, nil
			})
			if err != nil || failedAt.IsZero() || !alreadyReady {
				t.Fatalf("initial failure=%v other check ready=%v error=%v", failedAt, alreadyReady, err)
			}
		})
	}
}

func TestSandboxReadinessRejectsFailedProbeProcessesEvenWithValidOutput(t *testing.T) {
	for _, program := range []string{"podman", "ssh"} {
		for _, failure := range []struct {
			name   string
			status int
			err    error
		}{{name: "exit-status", status: 1}, {name: "runner-error", err: errors.New("process unavailable")}} {
			if program == "ssh" && failure.name == "exit-status" {
				continue // ssh cannot authenticate and exits non-zero; the signal is a recorded Ed25519 key together with NEWKEYS received
			}
			t.Run(program+"/"+failure.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				defer cancel()
				err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
					if request.Name == "podman" {
						fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
					} else {
						recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
					}
					if request.Name == program {
						return failure.status, failure.err
					}
					return 0, nil
				})
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error=%v", err)
				}
			})
		}
	}
}

func TestSandboxReadinessStopsBeforeProbingWhenCallerIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(context.Context, process.Request) (int, error) {
		t.Fatal("probe ran after caller cancellation")
		return 0, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestSandboxReadinessRejectsSuccessAfterCallerCancellation(t *testing.T) {
	for _, program := range []string{"podman", "ssh"} {
		t.Run(program, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
				} else {
					recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
				}
				if request.Name == program {
					cancel()
				}
				return 0, nil
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSandboxReadinessCutsProbeTimeoutToTheCallerDeadline(t *testing.T) {
	for _, blocked := range []string{"podman", "ssh"} {
		t.Run(blocked, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			parentDeadline, _ := ctx.Deadline()
			started := time.Now()
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(probeCtx context.Context, request process.Request) (int, error) {
				probeDeadline, ok := probeCtx.Deadline()
				if !ok || !probeDeadline.Equal(parentDeadline) {
					t.Fatalf("probe deadline=%v caller deadline=%v", probeDeadline, parentDeadline)
				}
				if request.Name == blocked {
					<-probeCtx.Done()
				}
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
				} else {
					recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
				}
				return 0, nil
			})
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
				t.Fatalf("elapsed=%v error=%v", time.Since(started), err)
			}
		})
	}
}

func TestSandboxReadinessRecognizesSSHKeyExchangeOnTheDefaultPort(t *testing.T) {
	err := sandbox.WaitReady(context.Background(), "sandboxed-agents.default.agent01", 22, func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager dev")
		} else {
			recordKnownHost(t, request, "# unrelated line\n127.0.0.1 "+readinessPublicKey+"\n")
		}
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// windowsInboxOpenSSH models the Windows OpenSSH 9.5 client pair observed against a sandbox sshd that
// offers sntrup761x25519: ssh-keyscan proposes sntrup761x25519 although the Windows build cannot compute
// it and fails (Win32-OpenSSH #2140), while ssh filters its proposal, records the host key under
// StrictHostKeyChecking=accept-new when it arrives, logs NEWKEYS received once the key exchange is
// verified, and then has its authentication denied.
func windowsInboxOpenSSH(t *testing.T, port string, sshCalls *[][]string) process.Runner {
	return func(_ context.Context, request process.Request) (int, error) {
		switch request.Name {
		case "podman":
			fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
			return 0, nil
		case "ssh-keyscan":
			fmt.Fprintln(request.Streams.Stderr, "choose_kex: unsupported KEX method sntrup761x25519-sha512@openssh.com")
			return 1, nil
		case "ssh":
			*sshCalls = append(*sshCalls, slices.Clone(request.Args))
			if slices.Contains(request.Args, "StrictHostKeyChecking=accept-new") {
				if err := testutil.RecordKnownHost(request.Dir, request.Args, fmt.Sprintf("[127.0.0.1]:%s %s\n", port, readinessPublicKey)); err != nil {
					t.Fatalf("known hosts file not prepared: %v", err)
				}
				fmt.Fprintln(request.Streams.Stderr, testutil.SSHNewKeysReceived)
			}
			fmt.Fprintln(request.Streams.Stderr, "agent@127.0.0.1: Permission denied (publickey).")
			return 255, nil
		}
		t.Fatalf("unexpected program %q", request.Name)
		return 0, nil
	}
}

func TestSandboxReadinessCompletesTheKeyExchangeWithTheInboxWindowsClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var sshCalls [][]string
	if err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, windowsInboxOpenSSH(t, "2222", &sshCalls)); err != nil {
		t.Fatalf("readiness failed with the inbox Windows client: %v", err)
	}
	if len(sshCalls) != 1 {
		t.Fatalf("SSH calls=%v", sshCalls)
	}
}

func knownHostsPath(t *testing.T, request process.Request) string {
	t.Helper()
	path, ok := testutil.KnownHostsFile(request.Dir, request.Args)
	if !ok {
		t.Fatalf("no UserKnownHostsFile in %v", request.Args)
	}
	return path
}

// recordKnownHost does what ssh -v does in a completed key exchange: it records the host key under
// StrictHostKeyChecking=accept-new when the key arrives and, after verifying the exchange signature,
// logs that it received the server's NEWKEYS.
func recordKnownHost(t *testing.T, request process.Request, content string) {
	t.Helper()
	recordHostKeyOnly(t, request, content)
	fmt.Fprintln(request.Streams.Stderr, testutil.SSHNewKeysReceived)
}

// recordHostKeyOnly models an exchange that stops after the host key callback, before the signature.
func recordHostKeyOnly(t *testing.T, request process.Request, content string) {
	t.Helper()
	if err := testutil.RecordKnownHost(request.Dir, request.Args, content); err != nil {
		t.Fatalf("known hosts file not prepared: %v", err)
	}
}

func TestSandboxReadinessKeyExchangeUsesNoUserSSHFilesOrCredentials(t *testing.T) {
	var path string
	err := sandbox.WaitReady(context.Background(), "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
			return 0, nil
		}
		path = knownHostsPath(t, request)
		want := []string{"-F", "none", "-v", "-T", "-n",
			"-o", "BatchMode=yes", "-o", "ConnectTimeout=1", "-o", "ConnectionAttempts=1",
			"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=known_hosts",
			"-o", "GlobalKnownHostsFile=none", "-o", "HashKnownHosts=no", "-o", "UpdateHostKeys=no", "-o", "CheckHostIP=no",
			"-o", "HostKeyAlgorithms=ssh-ed25519",
			"-o", "PubkeyAuthentication=no", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "IdentityAgent=none",
			"-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
			"-p", "2222", "-l", "agent", "--", "127.0.0.1", "true"}
		if !slices.Equal(request.Args, want) {
			t.Fatalf("SSH args=%v\nwant=%v", request.Args, want)
		}
		if filepath.Dir(filepath.Dir(path)) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "sandboxed-agents-readiness-") {
			t.Fatalf("known hosts file outside a private temporary directory: %s", path)
		}
		if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
			t.Fatalf("known hosts file not empty before the exchange: %q %v", data, err)
		}
		recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
		return 255, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("private known hosts directory left behind: %v", err)
	}
}

func TestSandboxReadinessRejectsAHostKeyWithoutACompletedKeyExchange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
			return 0, nil
		}
		recordHostKeyOnly(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
		fmt.Fprintln(request.Streams.Stderr, "debug1: SSH2_MSG_NEWKEYS sent")
		fmt.Fprintln(request.Streams.Stderr, "ssh_dispatch_run_fatal: Connection to 127.0.0.1 port 2222: incorrect signature")
		return 255, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}

// Git for Windows' ssh (MSYS2 runtime) splits a Windows command line differently from the inbox client, so
// a path in an ssh option can turn into several arguments or an unterminated quote ("invalid quotes").
// The probe therefore runs ssh in its private temporary directory and names the file relatively: the
// command line holds no path, whatever characters the temporary directory's path contains.
func TestSandboxReadinessKeepsTheKnownHostsPathOutOfTheSSHCommandLine(t *testing.T) {
	for _, dir := range []string{"plain", "100%", "with space", "50% done", "o'brien", `a"b`, `back\slash`, "tab\there"} {
		t.Run(dir, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.ContainsAny(dir, "\"\\\t") {
				t.Skip("not a valid Windows file name")
			}
			root := filepath.Join(t.TempDir(), dir)
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, root)
			}
			var argument, workDir string
			err := sandbox.WaitReady(context.Background(), "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
					return 0, nil
				}
				for i, arg := range request.Args {
					if i > 0 && request.Args[i-1] == "-o" && strings.HasPrefix(arg, "UserKnownHostsFile=") {
						argument = arg
					}
					if strings.Contains(arg, dir) {
						t.Errorf("argument %q holds the temporary path", arg)
					}
				}
				workDir = request.Dir
				recordKnownHost(t, request, "[127.0.0.1]:2222 "+readinessPublicKey+"\n")
				return 255, nil
			})
			if err != nil {
				t.Fatalf("the recorded host key was not read back: %v", err)
			}
			if argument != "UserKnownHostsFile=known_hosts" {
				t.Fatalf("argument=%q", argument)
			}
			if filepath.Dir(workDir) != root || !strings.HasPrefix(filepath.Base(workDir), "sandboxed-agents-readiness-") {
				t.Fatalf("working directory %q is not a private directory under %q", workDir, root)
			}
		})
	}
}

package platform_test

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestProcessGroupCleanupStopsChildrenAndBoundsOutputPipes(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		cancel     bool
		detached   bool
		want       error
	}{
		{"canceled", "parent", true, false, context.Canceled},
		{"parent exited", "parent-exit", false, false, exec.ErrWaitDelay},
		{"child leaves group", "parent-detached", true, true, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			done := make(chan error, 1)
			go func() {
				_, err := platform.Run(ctx, process.Request{
					Name: os.Args[0], Args: []string{"-test.run=^TestProcessGroupFixture$"},
					Env:          append(os.Environ(), "SANDBOXED_AGENTS_PROCESS_GROUP_FIXTURE="+test.mode),
					CleanupGroup: true,
					Streams:      process.Streams{Stdout: writer},
				})
				done <- err
			}()
			pidReady := make(chan int, 1)
			go func() {
				line, _ := bufio.NewReader(reader).ReadString('\n')
				pid, _ := strconv.Atoi(strings.TrimSpace(line))
				pidReady <- pid
			}()
			var pid int
			select {
			case pid = <-pidReady:
				if pid <= 0 {
					t.Fatal("process did not report its descendant PID")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("process never started its descendant")
			}
			defer syscall.Kill(pid, syscall.SIGKILL)
			if test.cancel {
				cancel()
			}
			select {
			case err := <-done:
				if err != test.want {
					t.Fatalf("cleanup result=%v", err)
				}
			case <-time.After(3 * time.Second):
				syscall.Kill(pid, syscall.SIGKILL)
				<-done
				t.Fatal("process kept waiting for its descendant's output pipes")
			}
			if test.detached {
				return
			}
			deadline := time.Now().Add(time.Second)
			for {
				data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
				if os.IsNotExist(err) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				_, fields, ok := strings.Cut(string(data), ") ")
				if ok && strings.HasPrefix(fields, "Z ") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("descendant remains live after process group cleanup: %s", data)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestProcessGroupFixture(t *testing.T) {
	switch os.Getenv("SANDBOXED_AGENTS_PROCESS_GROUP_FIXTURE") {
	case "child":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "parent", "parent-exit", "parent-detached":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessGroupFixture$")
		child.Env = append(os.Environ(), "SANDBOXED_AGENTS_PROCESS_GROUP_FIXTURE=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if os.Getenv("SANDBOXED_AGENTS_PROCESS_GROUP_FIXTURE") == "parent-detached" {
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		io.WriteString(os.Stdout, strconv.Itoa(child.Process.Pid)+"\n")
		if os.Getenv("SANDBOXED_AGENTS_PROCESS_GROUP_FIXTURE") == "parent-exit" {
			os.Exit(0)
		}
		child.Wait()
		os.Exit(0)
	}
}

package platform_test

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestRunPreservesSignalExitStatus(t *testing.T) {
	for _, test := range []struct {
		name string
		code int
	}{{"term", 143}, {"kill", 137}} {
		t.Run(test.name, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			code, err := platform.Run(ctx, process.Request{Name: executable, Args: []string{"-test.run=^TestSignalProcess$"}, Env: append(os.Environ(), "SANDBOXED_AGENTS_SIGNAL_FIXTURE="+test.name)})
			if err != nil || code != test.code {
				t.Fatalf("status=%d error=%v want=%d", code, err, test.code)
			}
		})
	}
}

func TestSignalProcess(t *testing.T) {
	var signal syscall.Signal
	switch os.Getenv("SANDBOXED_AGENTS_SIGNAL_FIXTURE") {
	case "term":
		signal = syscall.SIGTERM
	case "kill":
		signal = syscall.SIGKILL
	default:
		return
	}
	if err := syscall.Kill(os.Getpid(), signal); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Second)
	t.Fatal("process survived its signal")
}

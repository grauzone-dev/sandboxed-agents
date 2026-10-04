package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

const readinessPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea"

func TestSandboxReadinessRequiresManagerAndSSHKeyExchange(t *testing.T) {
	managerAnswered, sshAnswered := false, false
	err := sandbox.WaitReady(context.Background(), "sandboxed-agents.default.agent01", 2222, func(ctx context.Context, request process.Request) (int, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second || time.Until(deadline) <= 0 {
			t.Fatalf("probe deadline=%v present=%v", deadline, ok)
		}
		if request.Streams.Stdin != nil || request.User != nil || request.Dir != "" || request.Env != nil {
			t.Fatalf("unexpected probe settings: %+v", request)
		}
		switch request.Name {
		case "podman":
			if !slices.Equal(request.Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}) {
				t.Fatalf("manager args=%v", request.Args)
			}
			managerAnswered = true
			fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
		case "ssh-keyscan":
			if !slices.Equal(request.Args, []string{"-T", "1", "-t", "ed25519", "-p", "2222", "127.0.0.1"}) {
				t.Fatalf("SSH args=%v", request.Args)
			}
			sshAnswered = true
			fmt.Fprintln(request.Streams.Stdout, "[127.0.0.1]:2222 "+readinessPublicKey)
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
					fmt.Fprintln(request.Streams.Stdout, "[127.0.0.1]:2222 "+readinessPublicKey)
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
	for _, output := range []string{"", "# 127.0.0.1:2222 SSH-2.0-OpenSSH\n", "127.0.0.1 " + readinessPublicKey, "[127.0.0.1]:2223 " + readinessPublicKey, "[192.0.2.1]:2222 " + readinessPublicKey, "[127.0.0.1]:2222 ssh-ed25519 !!!", "[127.0.0.1]:2222 ssh-ed25519 YWJj", "[127.0.0.1]:2222 ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAINdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea", "[127.0.0.1]:2222 " + readinessPublicKey + "\fcomment"} {
		t.Run(fmt.Sprintf("%q", output), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
				} else {
					fmt.Fprint(request.Streams.Stdout, output)
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
	for _, unready := range []string{"podman", "ssh-keyscan"} {
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
					fmt.Fprintln(request.Streams.Stdout, "[127.0.0.1]:2222 "+readinessPublicKey)
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
	for _, program := range []string{"podman", "ssh-keyscan"} {
		for _, failure := range []struct {
			name   string
			status int
			err    error
		}{{name: "exit-status", status: 1}, {name: "runner-error", err: errors.New("process unavailable")}} {
			t.Run(program+"/"+failure.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				defer cancel()
				err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
					if request.Name == "podman" {
						fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
					} else {
						fmt.Fprintln(request.Streams.Stdout, "[127.0.0.1]:2222 "+readinessPublicKey)
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
	for _, program := range []string{"podman", "ssh-keyscan"} {
		t.Run(program, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := sandbox.WaitReady(ctx, "sandboxed-agents.default.agent01", 2222, func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager 0.1.0")
				} else {
					fmt.Fprintln(request.Streams.Stdout, "[127.0.0.1]:2222 "+readinessPublicKey)
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
	for _, blocked := range []string{"podman", "ssh-keyscan"} {
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
					fmt.Fprintln(request.Streams.Stdout, "[127.0.0.1]:2222 "+readinessPublicKey)
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
			fmt.Fprintln(request.Streams.Stdout, "# 127.0.0.1:22 SSH-2.0-OpenSSH")
			fmt.Fprintln(request.Streams.Stdout, "127.0.0.1 "+readinessPublicKey)
		}
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

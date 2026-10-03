package cli_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestWindowsOperationsStayOnTheCheckedConnection(t *testing.T) {
	for _, overrides := range []struct {
		name   string
		values map[string]string
	}{
		{"connection", map[string]string{"CONTAINER_CONNECTION": "unchecked-connection"}},
		{"host", map[string]string{"CONTAINER_HOST": "ssh://unchecked-host/run/podman.sock"}},
		{"combined", map[string]string{"CONTAINER_CONNECTION": "unchecked-connection", "CONTAINER_HOST": "ssh://unchecked-host/run/podman.sock", "CONTAINER_SSHKEY": "unchecked-key"}},
		{"saved default", nil},
	} {
		for _, machineList := range []struct{ name, json string }{
			{"default among two", `[{"Name":"other","Default":false,"Running":true,"VMType":"wsl"},{"Name":"checked-machine","Default":true,"Running":true,"VMType":"wsl"}]`},
			{"sole without default", `[{"Name":"checked-machine","Default":false,"Running":true,"VMType":"wsl"}]`},
		} {
			for _, command := range []string{"build", "up", "start", "stop", "restart", "remove", "shell"} {
				t.Run(overrides.name+"/"+machineList.name+"/"+command, func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					for _, name := range []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"} {
						t.Setenv(name, overrides.values[name])
					}
					preflight := healthyWindowsPodman()
					preflight[1].Stdout = machineList.json
					preflight[2].Stdout = `[{"Name":"checked-machine","State":"running","Rootful":false}]`
					var responses []testutil.Response
					var args []string
					var operations []string
					switch command {
					case "shell":
						responses = append(responses, preflight[1:3]...)
						owned := "default"
						responses = append(responses, sandboxObjectResponses(&owned, true, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)...)
						responses = append(responses, testutil.Response{})
						args = []string{command, "agent01"}
						operations = []string{"exec"}
					case "build":
						responses = append(preflight, testutil.Response{}, testutil.Response{Stdout: "[]"})
						args = []string{command}
						operations = []string{"build"}
					case "up":
						responses = append(preflight, upObjectResponses(nil, false, nil, nil)[1:]...)
						responses = append(responses, testutil.Response{ExitCode: 1}, testutil.Response{})
						responses = append(responses, make([]testutil.Response, 5)...)
						args = []string{command, "agent01"}
						operations = []string{"build", "create", "start"}
					default:
						responses = append(responses, preflight[1:3]...)
						owned := "default"
						responses = append(responses, sandboxObjectResponses(&owned, command != "start", map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)...)
						if command != "start" {
							responses = append(responses, testutil.Response{Stdout: "[]"})
						}
						operations = []string{command}
						if command == "restart" {
							operations = []string{"stop", "start"}
						} else if command == "remove" {
							operations = []string{"stop", "rm"}
						}
						responses = append(responses, make([]testutil.Response, len(operations))...)
						args = []string{command, "agent01"}
					}
					for index := range responses {
						responses[index].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
					}
					fakes.Script("podman", responses...)
					stdout, stderr, status := runCLI(t, "windows", args...)
					if status != 0 || stderr != "" {
						t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
					}
					calls := fakes.Calls("podman")
					selectionCalls := 2
					if command == "build" || command == "up" {
						selectionCalls = 7
					}
					if len(calls) <= selectionCalls {
						t.Fatalf("no sandbox operation: %v", calls)
					}
					seen := map[string]bool{}
					for _, call := range windowsOperationCalls(t, calls[selectionCalls:], "checked-machine") {
						seen[call.Args[0]] = true
					}
					for _, operation := range operations {
						if !seen[operation] {
							t.Fatalf("missing %s: %v", operation, calls)
						}
					}
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestWindowsLifecycleKeepsOwnershipAndSessionGuardsOnItsTarget(t *testing.T) {
	for _, command := range []string{"stop", "restart", "remove"} {
		for _, refusal := range []string{"owner", "sessions", "manager"} {
			t.Run(command+"/"+refusal, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner := "default"
				if refusal == "owner" {
					owner = "other"
				}
				responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
				responses = append(responses, sandboxObjectResponses(&owner, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)...)
				if refusal == "sessions" {
					responses = append(responses, testutil.Response{Stdout: `[{"name":"work","agent":"codex"}]`})
				} else if refusal == "manager" {
					responses = append(responses, testutil.Response{ExitCode: 1})
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "windows", command, "agent01")
				if status == 0 || stdout != "" || stderr == "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := fakes.Calls("podman")
				for _, call := range windowsOperationCalls(t, calls[2:], "podman-machine-default") {
					if call.Args[0] != "container" && call.Args[0] != "volume" && call.Args[0] != "exec" {
						t.Fatalf("refused command mutated objects: %v", call.Args)
					}
					if call.Args[0] == "exec" {
						if refusal == "owner" || !reflect.DeepEqual(call.Args, sessionQueryArgs()) {
							t.Fatalf("unexpected manager query: %v", call.Args)
						}
					} else if len(call.Args) < 2 || call.Args[1] != "exists" && call.Args[1] != "inspect" {
						t.Fatalf("refused command mutated objects: %v", call.Args)
					}
				}
			})
		}
	}
}

func TestWindowsLifecycleRefusesAnUnavailableTargetBeforeObjectLookups(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart", "remove", "shell"} {
		for _, target := range []struct{ name, list, inspect string }{
			{"absent", `[]`, ""},
			{"ambiguous", `[{"Name":"one","Running":true,"VMType":"wsl"},{"Name":"two","Running":true,"VMType":"wsl"}]`, ""},
			{"duplicate defaults", `[{"Name":"one","Default":true,"Running":true,"VMType":"wsl"},{"Name":"two","Default":true,"Running":true,"VMType":"wsl"}]`, ""},
			{"stopped", `[{"Name":"one","Default":true,"Running":false,"VMType":"wsl"}]`, `[{"Name":"one","State":"stopped","Rootful":false}]`},
			{"rootful", `[{"Name":"one","Default":true,"Running":true,"VMType":"wsl"}]`, `[{"Name":"one","State":"running","Rootful":true}]`},
			{"unknown rootless", `[{"Name":"one","Default":true,"Running":true,"VMType":"wsl"}]`, `[{"Name":"one","State":"running"}]`},
			{"wrong provider", `[{"Name":"one","Default":true,"Running":true,"VMType":"hyperv"}]`, `[{"Name":"one","State":"running","Rootful":false}]`},
			{"unreadable", "not-json", ""},
			{"unreadable inspect", `[{"Name":"one","Default":true,"Running":true,"VMType":"wsl"}]`, "not-json"},
			{"option name", `[{"Name":"--all","Default":true,"Running":true,"VMType":"wsl"}]`, ""},
			{"control name", `[{"Name":"bad\nname","Default":true,"Running":true,"VMType":"wsl"}]`, ""},
			{"carriage return name", `[{"Name":"bad\rname","Default":true,"Running":true,"VMType":"wsl"}]`, ""},
			{"nul name", `[{"Name":"bad\u0000name","Default":true,"Running":true,"VMType":"wsl"}]`, ""},
		} {
			t.Run(fmt.Sprintf("%s/%s", command, target.name), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := []testutil.Response{{Stdout: target.list}}
				if target.inspect != "" {
					responses = append(responses, testutil.Response{Stdout: target.inspect})
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "windows", command, "agent01")
				if status == 0 || stdout != "" || !strings.Contains(stderr, "sandboxed-agents check") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, call := range fakes.Calls("podman") {
					if len(call.Args) < 2 || call.Args[0] != "machine" || (call.Args[1] != "list" && call.Args[1] != "inspect") {
						t.Fatalf("looked up or changed objects without target: %v", call.Args)
					}
				}
			})
		}
	}
}

func TestWindowsPreflightRefusesAmbiguousAndUnsafeTargets(t *testing.T) {
	for _, machineList := range []string{
		`[{"Name":"one","Default":true,"Running":true,"VMType":"wsl"},{"Name":"two","Default":true,"Running":true,"VMType":"wsl"}]`,
		`[{"Name":"--all","Default":true,"Running":true,"VMType":"wsl"}]`,
		`[{"Name":"bad\nname","Default":true,"Running":true,"VMType":"wsl"}]`,
		`[{"Name":"bad\rname","Default":true,"Running":true,"VMType":"wsl"}]`,
		`[{"Name":"bad\u0000name","Default":true,"Running":true,"VMType":"wsl"}]`,
	} {
		for _, command := range []string{"check", "build", "up"} {
			t.Run(command+"/"+machineList, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				fakes.Script("podman", healthyWindowsPodman()[0], testutil.Response{Stdout: machineList})
				args := []string{command}
				if command == "up" {
					args = append(args, "agent01")
				}
				_, stderr, status := runCLI(t, "windows", args...)
				if status == 0 || stderr == "" {
					t.Fatalf("status=%d stderr=%q", status, stderr)
				}
				calls := fakes.Calls("podman")
				if len(calls) != 2 || !reflect.DeepEqual(calls[0].Args, []string{"--version"}) || !reflect.DeepEqual(calls[1].Args, []string{"machine", "list", "--format", "json"}) {
					t.Fatalf("unsafe target was probed or used: %v", calls)
				}
			})
		}
	}
}

func TestWindowsLifecycleUsageDoesNotResolveATarget(t *testing.T) {
	for _, args := range [][]string{{"start"}, {"stop", ".bad"}, {"restart", "agent01", "--unknown"}, {"remove", "agent01", "--force", "--force"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			_, stderr, status := runCLI(t, "windows", args...)
			if status == 0 || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stderr=%q calls=%v", status, stderr, fakes.Calls("podman"))
			}
		})
	}
}

func windowsOperationCalls(t *testing.T, calls []testutil.Call, connection string) []testutil.Call {
	t.Helper()
	operations := make([]testutil.Call, len(calls))
	for index, call := range calls {
		if len(call.Args) < 3 || !reflect.DeepEqual(call.Args[:2], []string{"--connection", connection}) {
			t.Fatalf("operation reached unchecked connection: %v", call.Args)
		}
		operations[index] = testutil.Call{Args: call.Args[2:]}
	}
	return operations
}

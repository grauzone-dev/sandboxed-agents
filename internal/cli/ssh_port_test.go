package cli_test

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpPublishesOnlyLoopbackSSHAndRecordsItsPort(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:2222")
	if err != nil {
		t.Skipf("port 2222 unavailable: %v", err)
	}
	listener.Close()
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := upObjectResponses(nil, false, nil, nil)
			responses = append(responses, make([]testutil.Response, 6)...)
			scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			create := fakes.Calls("podman")[len(fakes.Calls("podman"))-2].Args
			if !slices.Contains(create, "io.github.sandboxed-agents.ssh-port=2222") {
				t.Fatalf("missing SSH port label: %v", create)
			}
			var published []string
			for i, arg := range create {
				if arg == "--publish" || arg == "-p" {
					published = append(published, create[i+1])
				}
				if strings.HasPrefix(arg, "--publish=") {
					published = append(published, strings.TrimPrefix(arg, "--publish="))
				}
			}
			if !slices.Equal(published, []string{"127.0.0.1:2222:22"}) {
				t.Fatalf("published=%v", published)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpAcceptsExplicitSSHPortAlongsideResourceLimits(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			port := unusedSSHPort(t)
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := upObjectResponses(nil, false, nil, nil)
			responses = append(responses, make([]testutil.Response, 6)...)
			scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01", "--memory=12g", "--port", strconv.Itoa(port), "--cpus", "2")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			create := fakes.Calls("podman")[len(fakes.Calls("podman"))-2].Args
			assertSSHPublication(t, create, port)
			if !slices.Contains(create, "--memory=12884901888") || !slices.Contains(create, "--cpus=2") {
				t.Fatalf("resource options lost: %v", create)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func unusedSSHPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func assertSSHPublication(t *testing.T, create []string, port int) {
	t.Helper()
	if !slices.Contains(create, "io.github.sandboxed-agents.ssh-port="+strconv.Itoa(port)) {
		t.Fatalf("missing SSH port label: %v", create)
	}
	var published []string
	for i, arg := range create {
		if arg == "--publish" || arg == "-p" {
			if i+1 >= len(create) {
				t.Fatal("missing publication value")
			}
			published = append(published, create[i+1])
		}
		if strings.HasPrefix(arg, "--publish=") {
			published = append(published, strings.TrimPrefix(arg, "--publish="))
		}
	}
	if !slices.Equal(published, []string{fmt.Sprintf("127.0.0.1:%d:22", port)}) {
		t.Fatalf("published=%v", published)
	}
}

func TestStartAndRestartRefuseAnUnavailableRecordedSSHPort(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"start", "restart"} {
			t.Run(host.name+"/"+command, func(t *testing.T) {
				listener, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				port := listener.Addr().(*net.TCPAddr).Port
				fakes, fixture := resourceLimitHost(t, host.windows)
				owned := "default"
				responses := upObjectResponses(&owned, false, nil, nil)
				if host.windows {
					responses = append([]testutil.Response{healthyWindowsPodman()[0]}, responses[1:]...)
				}
				scriptResourceLimitObjects(t, fakes, false, responses, map[string]string{"ssh-port": strconv.Itoa(port)})
				if host.windows {
					fakes.Script("podman", append(healthyWindowsPodman()[1:3], responses[1:]...)...)
				} else {
					fakes.Script("podman", responses[1:]...)
				}
				_, stderr, status := runCLI(t, fixture, command, "agent01")
				if status == 0 || !strings.Contains(stderr, strconv.Itoa(port)) || !strings.Contains(stderr, "unavailable") {
					t.Fatalf("status=%d stderr=%q", status, stderr)
				}
				assertSSHReadOnly(t, fakes)
			})
		}
	}
}

func assertSSHReadOnly(t *testing.T, fakes *testutil.FakePrograms) {
	t.Helper()
	for _, call := range fakes.Calls("podman") {
		args := call.Args
		if len(args) > 2 && args[0] == "--connection" {
			args = args[2:]
		}
		if slices.Equal(args, []string{"--version"}) || slices.Equal(args, []string{"ps", "--all", "--format", "json"}) {
			continue
		}
		if len(args) >= 2 && args[0] == "machine" && (args[1] == "list" || args[1] == "inspect" || args[1] == "ssh") {
			continue
		}
		if slices.Equal(args, []string{"version", "--format", "json"}) || slices.Equal(args, []string{"info", "--format", "json"}) {
			continue
		}
		if len(args) == 3 && (args[0] == "container" || args[0] == "volume") && (args[1] == "exists" || args[1] == "inspect") {
			continue
		}
		t.Fatalf("refused command mutated Podman state: %v", call.Args)
	}
	assertNoSSH(t, fakes)
}

func firstFreeSSHPort(t *testing.T) int {
	t.Helper()
	for port := 2222; port <= 65535; port++ {
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		listener.Close()
		return port
	}
	t.Fatal("no available SSH test port")
	return 0
}

func TestListShowsRecordedSSHPortsForRunningAndStoppedSandboxes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := []testutil.Response{
				{Stdout: `[{"Names":["sandboxed-agents.default.running"],"Labels":{"io.github.sandboxed-agents.owner":"default"}},{"Names":["sandboxed-agents.default.stopped"],"Labels":{"io.github.sandboxed-agents.owner":"default"}}]`},
				{Stdout: `[]`},
				{Stdout: `[{"Name":"sandboxed-agents.default.running","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.ssh-port":"2300"}},"State":{"Running":true}}]`},
				{Stdout: `[{"Name":"sandboxed-agents.default.stopped","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.ssh-port":"2301"}},"State":{"Running":false}}]`},
			}
			if host.windows {
				responses = append(healthyWindowsPodman()[1:3], responses...)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, fixture, "list")
			normalized := strings.Join(strings.Fields(stdout), " ")
			if status != 0 || stderr != "" || !strings.Contains(normalized, "SSH PORT") || !strings.Contains(normalized, "running running - 2300 - - -") || !strings.Contains(normalized, "stopped stopped - 2301 - - -") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertListReadOnly(t, fakes, host.windows)
		})
	}
}

func TestUpRejectsInvalidSSHPortOptionsBeforeExternalCalls(t *testing.T) {
	invalid := [][]string{{"--port"}, {"--port="}, {"--port", "0"}, {"--port", "65536"}, {"--port", "-1"}, {"--port", "+2300"}, {"--port", "2.3"}, {"--port", "2e3"}, {"--port", " 2300"}, {"--port", "2300 "}, {"--port", "999999999999999999999"}, {"--port", "2300", "--port=2300"}, {"--memory", "--port", "2300"}}
	for _, host := range resourceLimitHosts {
		for _, args := range invalid {
			t.Run(host.name+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				stdout, stderr, status := runCLI(t, fixture, append([]string{"up", "agent01"}, args...)...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") || !strings.Contains(stderr, "--") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if len(fakes.Calls("podman")) != 0 {
					t.Fatal("invalid usage called Podman")
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestUpRejectsAnExplicitUnavailableSSHPortBeforeCreatingAnything(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, reason := range []string{"listener", "recorded"} {
			t.Run(host.name+"/"+reason, func(t *testing.T) {
				listener, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				port := listener.Addr().(*net.TCPAddr).Port
				inventory := "[]"
				if reason == "recorded" {
					listener.Close()
					inventory = fmt.Sprintf(`[{"Names":["sandboxed-agents.other.stopped"],"Labels":{"io.github.sandboxed-agents.owner":"other","io.github.sandboxed-agents.ssh-port":%q},"State":"exited"}]`, strconv.Itoa(port))
				}
				fakes, fixture := resourceLimitHost(t, host.windows)
				responses := upObjectResponses(nil, false, nil, nil)
				responses[len(responses)-1] = testutil.Response{Stdout: inventory}
				scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
				_, stderr, status := runCLI(t, fixture, "up", "agent01", "--port", strconv.Itoa(port))
				if status == 0 || !strings.Contains(stderr, strconv.Itoa(port)) {
					t.Fatalf("status=%d stderr=%q", status, stderr)
				}
				assertSSHReadOnly(t, fakes)
				calls := fakes.Calls("podman")
				last := calls[len(calls)-1].Args
				if host.windows {
					last = windowsOperationCalls(t, calls[len(calls)-1:], "podman-machine-default")[0].Args
				}
				if !slices.Equal(last, []string{"ps", "--all", "--format", "json"}) {
					t.Fatalf("missing recorded-port inventory: %v", calls)
				}
			})
		}
	}
}

func TestUpSkipsListenersAndRecordedSSHPortsAcrossControllerGroups(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, state := range []string{"running", "exited"} {
			t.Run(host.name+"/"+state, func(t *testing.T) {
				busy := firstFreeSSHPort(t)
				listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", busy))
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				reserved := firstFreeSSHPort(t)
				probe, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", reserved))
				if err != nil {
					t.Fatal(err)
				}
				expected := firstFreeSSHPort(t)
				probe.Close()
				fakes, fixture := resourceLimitHost(t, host.windows)
				inventory := fmt.Sprintf(`[{"Names":["sandboxed-agents.other.agent01"],"Labels":{"io.github.sandboxed-agents.owner":"other","io.github.sandboxed-agents.ssh-port":%q},"State":%q},{"Names":["unrelated-service"],"Labels":{"io.github.sandboxed-agents.ssh-port":%q}}]`, strconv.Itoa(reserved), state, strconv.Itoa(expected))
				responses := upObjectResponses(nil, false, nil, nil)
				responses[len(responses)-1] = testutil.Response{Stdout: inventory}
				responses = append(responses, make([]testutil.Response, 6)...)
				scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
				stdout, stderr, status := runCLI(t, fixture, "up", "agent01")
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := fakes.Calls("podman")
				if host.windows {
					calls = windowsOperationCalls(t, calls[7:], "podman-machine-default")
				}
				assertSSHPublication(t, calls[len(calls)-2].Args, expected)
				for _, call := range calls {
					if slices.Contains(call.Args, "sandboxed-agents.other.agent01") || slices.Contains(call.Args, "unrelated-service") {
						t.Fatalf("allocation changed or inspected unrelated container: %v", call.Args)
					}
				}
				if !slices.Equal(calls[len(calls)-7].Args, []string{"ps", "--all", "--format", "json"}) {
					t.Fatalf("missing read-only inventory: %v", calls)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestUpKeepsExistingSSHPortAndRefusesAChange(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{false, true} {
			for _, option := range []string{"absent", "equal", "different"} {
				t.Run(fmt.Sprintf("%s/running-%t/%s", host.name, running, option), func(t *testing.T) {
					port := unusedSSHPort(t)
					fakes, fixture := resourceLimitHost(t, host.windows)
					owned := "default"
					responses := upObjectResponses(&owned, running, nil, nil)
					if !running && option != "different" {
						responses = append(responses, testutil.Response{})
					}
					scriptResourceLimitObjects(t, fakes, host.windows, responses, map[string]string{"ssh-port": strconv.Itoa(port)})
					args := []string{"up", "agent01"}
					requested := port
					if option == "different" {
						if port == 65535 {
							requested--
						} else {
							requested++
						}
					}
					if option != "absent" {
						args = append(args, "--port="+strconv.Itoa(requested))
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if option == "different" {
						if status == 0 || !strings.Contains(stderr, strconv.Itoa(port)) || !strings.Contains(stderr, strconv.Itoa(requested)) || !strings.Contains(stderr, "remove agent01") || !strings.Contains(stderr, "up agent01") {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						assertSSHReadOnly(t, fakes)
					} else {
						if status != 0 || stderr != "" {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						calls := fakes.Calls("podman")
						var starts int
						for _, call := range calls {
							args := call.Args
							if host.windows && len(args) > 2 && args[0] == "--connection" {
								args = args[2:]
							}
							if args[0] == "start" {
								starts++
								if !slices.Equal(args, []string{"start", "sandboxed-agents.default.agent01"}) {
									t.Fatalf("changed recorded configuration: %v", args)
								}
							}
							if args[0] == "create" || args[0] == "ps" {
								t.Fatalf("existing sandbox allocated a new port: %v", args)
							}
						}
						expected := 1
						if running {
							expected = 0
						}
						if starts != expected {
							t.Fatalf("starts=%d", starts)
						}
						assertNoSSH(t, fakes)
					}
				})
			}
		}
	}
}

func TestRunningSandboxKeepsItsSSHListenerWhenUpOrStartAlreadyHolds(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"up", "start"} {
			t.Run(host.name+"/"+command, func(t *testing.T) {
				listener, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				port := listener.Addr().(*net.TCPAddr).Port
				fakes, fixture := resourceLimitHost(t, host.windows)
				owned := "default"
				responses := upObjectResponses(&owned, true, nil, nil)
				scriptResourceLimitObjects(t, fakes, host.windows && command == "up", responses, map[string]string{"ssh-port": strconv.Itoa(port)})
				if command == "start" {
					if host.windows {
						fakes.Script("podman", append(healthyWindowsPodman()[1:3], responses[1:]...)...)
					} else {
						fakes.Script("podman", responses[1:]...)
					}
				}
				stdout, stderr, status := runCLI(t, fixture, command, "agent01")
				if status != 0 || stderr != "" || !strings.Contains(stdout, "is running") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				assertSSHReadOnly(t, fakes)
			})
		}
	}
}

func TestUpAllocatesDifferentSSHPortsAfterTheFirstSandboxStops(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			first := firstFreeSSHPort(t)
			probe, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", first))
			if err != nil {
				t.Fatal(err)
			}
			second := firstFreeSSHPort(t)
			probe.Close()
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...)
			scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			assertSSHPublication(t, calls[len(calls)-2].Args, first)
			responses = upObjectResponses(nil, false, nil, nil)
			responses[len(responses)-1] = testutil.Response{Stdout: fmt.Sprintf(`[{"Names":["sandboxed-agents.default.agent01"],"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.ssh-port":%q},"State":"exited"}]`, strconv.Itoa(first))}
			responses = append(responses, make([]testutil.Response, 6)...)
			scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
			stdout, stderr, status = runCLI(t, fixture, "up", "agent02")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls = fakes.Calls("podman")
			assertSSHPublication(t, calls[len(calls)-2].Args, second)
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpRefusesUnreadableSSHPortInventoryBeforeCreation(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, response := range []testutil.Response{
			{ExitCode: 125, Stderr: "port inventory inaccessible"},
			{Stdout: "not-json"}, {Stdout: "null"}, {Stdout: "{}"}, {Stdout: "[] trailing"}, {Stdout: `[{}]`},
			{Stdout: `[{"Names":[]}]`}, {Stdout: `[{"Names":[""]}]`}, {Stdout: `[{"Names":["x","y"]}]`}, {Stdout: `[{"Names":["x"]},{"Names":["x"]}]`},
			{Stdout: `[{"Names":["sandboxed-agents.other.agent"],"Labels":{"io.github.sandboxed-agents.ssh-port":"0"}}]`},
			{Stdout: `[{"Names":["sandboxed-agents.other.agent"],"Labels":{"io.github.sandboxed-agents.ssh-port":"65536"}}]`},
			{Stdout: `[{"Names":["sandboxed-agents.other.agent"],"Labels":{"io.github.sandboxed-agents.ssh-port":2300}}]`},
		} {
			t.Run(host.name+"/"+response.Stdout, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				responses := upObjectResponses(nil, false, nil, nil)
				responses[len(responses)-1] = response
				scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
				_, stderr, status := runCLI(t, fixture, "up", "agent01")
				if status == 0 || stderr == "" {
					t.Fatalf("status=%d stderr=%q", status, stderr)
				}
				assertSSHReadOnly(t, fakes)
			})
		}
	}
}

func TestUpSSHPortBoundsAndHelpParseWithoutPodman(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, value := range []string{"1", "65535", "02300"} {
			t.Run(host.name+"/"+value, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				_, stderr, status := runCLI(t, fixture, "up", "agent01", "--port="+value, "--help")
				if status != 0 || stderr != "" || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stderr=%q calls=%v", status, stderr, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestListShowsBackupSSHPortWhenWorkspaceVolumesRemain(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := listOneSandboxResponses("default", "agent01", nil, false, map[string]string{"workspace": "default"}, &owned)
	for i := range responses {
		responses[i].Stdout = strings.ReplaceAll(responses[i].Stdout, `"io.github.sandboxed-agents.workspace-kind":"volume"`, `"io.github.sandboxed-agents.workspace-kind":"volume","io.github.sandboxed-agents.ssh-port":"2300"`)
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "agent01 update interrupted volume 2300 - - sandboxed-agents.default.agent01.workspace") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertListReadOnly(t, fakes, false)
}

func TestUpStartAndRestartRefuseInvalidRecordedSSHPorts(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"up", "start", "restart"} {
			for _, recorded := range []string{"missing", "", "not-a-port", "0", "65536"} {
				t.Run(host.name+"/"+command+"/"+recorded, func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					owned := "default"
					responses := upObjectResponses(&owned, false, nil, nil)
					labels := `"io.github.sandboxed-agents.owner":"default"`
					if recorded != "missing" {
						labels += fmt.Sprintf(`,"io.github.sandboxed-agents.ssh-port":%q`, recorded)
					}
					responses[resourceContainerInspectResponse].Stdout = fmt.Sprintf(`[{"Name":"sandboxed-agents.default.agent01","Config":{"Labels":{%s}},"State":{"Running":false}}]`, labels)
					if command == "up" {
						scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
					} else {
						responses = responses[1:]
						if host.windows {
							responses = append(healthyWindowsPodman()[1:3], responses...)
						}
						fakes.Script("podman", responses...)
					}
					stdout, stderr, status := runCLI(t, fixture, command, "agent01")
					if status == 0 || !strings.Contains(stderr, "recorded SSH port") || !strings.Contains(stderr, "remove agent01") || !strings.Contains(stderr, "up agent01") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					assertSSHReadOnly(t, fakes)
				})
			}
		}
	}
}

func TestRestartRefusesAnSSHPortStillUnavailableAfterStopping(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			fakes, fixture := resourceLimitHost(t, host.windows)
			owned := "default"
			responses := upObjectResponses(&owned, true, nil, nil)
			scriptResourceLimitObjects(t, fakes, false, responses, map[string]string{"ssh-port": strconv.Itoa(port)})
			responses = append(responses[1:], testutil.Response{Stdout: "[]"}, testutil.Response{})
			if host.windows {
				responses = append(healthyWindowsPodman()[1:3], responses...)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, fixture, "restart", "agent01")
			if status == 0 || !strings.Contains(stderr, strconv.Itoa(port)) || !strings.Contains(stderr, "unavailable") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if host.windows {
				calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
			}
			if len(calls) < 2 || !slices.Equal(calls[len(calls)-2].Args, sessionQueryArgs()) || !slices.Equal(calls[len(calls)-1].Args, []string{"stop", "sandboxed-agents.default.agent01"}) {
				t.Fatalf("restart did not stop before refusing the unavailable port: %v", calls)
			}
			for _, call := range calls {
				args := call.Args
				if len(args) == 3 && (args[0] == "container" || args[0] == "volume") && (args[1] == "exists" || args[1] == "inspect") {
					continue
				}
				if slices.Equal(args, sessionQueryArgs()) || slices.Equal(args, []string{"stop", "sandboxed-agents.default.agent01"}) {
					continue
				}
				t.Fatalf("restart changed the port or attempted to start: %v", args)
			}
			assertNoSSH(t, fakes)
		})
	}
}

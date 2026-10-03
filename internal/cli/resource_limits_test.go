package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

const (
	resourceWindowsPreflightCalls    = 7
	resourceContainerInspectResponse = 2
)

var resourceLimitHosts = []struct {
	name    string
	windows bool
}{{"linux", false}, {"windows", true}}

func TestUpCreatesSandboxWithResourceOverrides(t *testing.T) {
	for _, host := range resourceLimitHosts {
		windows := host.windows
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, windows)
			scriptResourceLimitObjects(t, fakes, windows, append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...), nil)
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01", "--memory", "12g", "--cpus", "2.5", "--pids-limit", "512", "--shm-size", "256m")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			create := calls[len(calls)-2].Args
			for _, want := range []string{
				"--memory=12884901888", "--cpus=2.5", "--pids-limit=512", "--shm-size=268435456",
				"io.github.sandboxed-agents.memory=12884901888", "io.github.sandboxed-agents.cpus=2.5",
				"io.github.sandboxed-agents.pids-limit=512", "io.github.sandboxed-agents.shm-size=268435456",
			} {
				if !slices.Contains(create, want) {
					t.Errorf("create lacks %q: %v", want, create)
				}
			}
			assertNoSSH(t, fakes)
		})
	}

}

func resourceLimitHost(t *testing.T, windows bool) (*testutil.FakePrograms, string) {
	t.Helper()
	if windows {
		return testutil.NewFakePrograms(t), "windows"
	}
	return linuxHost(t), "linux-preflight"
}

func scriptResourceLimitObjects(t *testing.T, fakes *testutil.FakePrograms, windows bool, responses []testutil.Response, labels map[string]string) {
	t.Helper()
	if labels != nil {
		var records []struct {
			Name   string
			Config struct{ Labels map[string]string }
			State  struct{ Running bool }
		}
		if err := json.Unmarshal([]byte(responses[resourceContainerInspectResponse].Stdout), &records); err != nil {
			t.Fatal(err)
		}
		for key, value := range labels {
			records[0].Config.Labels["io.github.sandboxed-agents."+key] = value
		}
		encoded, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		responses[resourceContainerInspectResponse].Stdout = string(encoded)
	}
	if windows {
		responses = append(healthyWindowsPodman(), responses[1:]...)
	}
	fakes.Script("podman", responses...)
}

func TestUpKeepsUnspecifiedResourceDefaults(t *testing.T) {
	for _, host := range resourceLimitHosts {
		windows := host.windows
		for _, test := range []struct {
			option, value string
			want          []string
		}{
			{"", "", []string{"--memory=8589934592", "--cpus=4", "--pids-limit=2048", "--shm-size=1073741824"}},
			{"--memory", "16G", []string{"--memory=17179869184", "--cpus=4", "--pids-limit=2048", "--shm-size=1073741824"}},
			{"--cpus", "0.125", []string{"--memory=8589934592", "--cpus=0.125", "--pids-limit=2048", "--shm-size=1073741824"}},
			{"--pids-limit", "01024", []string{"--memory=8589934592", "--cpus=4", "--pids-limit=1024", "--shm-size=1073741824"}},
			{"--shm-size", "512m", []string{"--memory=8589934592", "--cpus=4", "--pids-limit=2048", "--shm-size=536870912"}},
		} {
			t.Run(test.option+"/"+host.name, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, windows)
				scriptResourceLimitObjects(t, fakes, windows, append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...), nil)
				args := []string{"up", "agent01"}
				if test.option != "" {
					args = append(args, test.option+"="+test.value)
				}
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := fakes.Calls("podman")
				create := calls[len(calls)-2].Args
				for _, want := range test.want {
					if !slices.Contains(create, want) {
						t.Errorf("create lacks %q: %v", want, create)
					}
					option, value, _ := strings.Cut(want, "=")
					label := "io.github.sandboxed-agents." + strings.TrimPrefix(option, "--") + "=" + value
					if !slices.Contains(create, label) {
						t.Errorf("create lacks %q: %v", label, create)
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestUpStartsExistingSandboxWithEquivalentResourceOptions(t *testing.T) {
	for _, host := range resourceLimitHosts {
		windows := host.windows
		for _, args := range [][]string{
			nil,
			{"--memory", "12288m", "--cpus", "02.500", "--pids-limit", "0512", "--shm-size", "268435456"},
			{"--cpus=2.50"},
		} {
			for _, running := range []bool{false, true} {
				t.Run(strings.Join(args, " ")+"/"+host.name+map[bool]string{false: "/stopped", true: "/running"}[running], func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, windows)
					owned := "default"
					responses := upObjectResponses(&owned, running, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)
					if !running {
						responses = append(responses, testutil.Response{})
					}
					scriptResourceLimitObjects(t, fakes, windows, responses, map[string]string{"memory": "12884901888", "cpus": "2.5", "pids-limit": "512", "shm-size": "268435456"})
					stdout, stderr, status := runCLI(t, fixture, append([]string{"up", "agent01"}, args...)...)
					if status != 0 || stderr != "" {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					calls := fakes.Calls("podman")
					if !running {
						if !slices.Equal(calls[len(calls)-1].Args, []string{"start", "sandboxed-agents.default.agent01"}) {
							t.Fatalf("calls=%v", calls)
						}
						calls = calls[:len(calls)-1]
					}
					assertResourceLimitCallsReadOnly(t, calls, windows)
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestUpRejectsInvalidResourceOptionsBeforePodman(t *testing.T) {
	for _, test := range []struct {
		option string
		values []string
	}{
		{"--memory", []string{"", "0", "1m", "-8g", "+8g", "1.5g", "8gb", "8GiB", " 8g", "8g ", "9223372036854775808", "8388608t"}},
		{"--cpus", []string{"", "0", "0.000", "-1", "+1", "1e2", "NaN", "Inf", ".5", "1.", "1.0001", "9223372036.855"}},
		{"--pids-limit", []string{"", "0", "-1", "1.5", "+1", "1k", "1e2", "9223372036854775808"}},
		{"--shm-size", []string{"", "0", "-1g", "1.5m", "1MiB", "9999999999999999999999g"}},
	} {
		for _, value := range test.values {
			t.Run(test.option+"="+value, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, "windows-arm64", "up", "agent01", test.option+"="+value)
				if status == 0 || stdout != "" || !strings.Contains(stderr, test.option) || !strings.Contains(stderr, "Usage:") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if len(fakes.Calls("podman")) != 0 {
					t.Fatal("usage error called Podman")
				}
				assertNoSSH(t, fakes)
			})
		}
	}
	for _, args := range [][]string{{"--memory"}, {"--memory", "--cpus", "2"}, {"--cpus=2", "--cpus", "2"}, {"--memory=8g", "--memory=8g"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "windows-arm64", append([]string{"up", "agent01"}, args...)...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("usage error called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpRefusesResourceConflictsWithoutChangingSandbox(t *testing.T) {
	for _, host := range resourceLimitHosts {
		windows := host.windows
		for _, test := range []struct{ option, recorded, given string }{
			{"--memory", "8589934592", "16g"},
			{"--cpus", "4", "2.5"},
			{"--pids-limit", "2048", "512"},
			{"--shm-size", "1073741824", "512m"},
		} {
			for _, running := range []bool{false, true} {
				t.Run(test.option+"/"+host.name+map[bool]string{false: "/stopped", true: "/running"}[running], func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, windows)
					owned := "default"
					responses := upObjectResponses(&owned, running, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)
					scriptResourceLimitObjects(t, fakes, windows, responses, map[string]string{strings.TrimPrefix(test.option, "--"): test.recorded})
					stdout, stderr, status := runCLI(t, fixture, "up", "agent01", test.option, test.given)
					if status == 0 || strings.Contains(stdout, "Sandbox agent01 is running") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					for _, want := range []string{test.option, test.recorded, test.given, "remove agent01", "up agent01"} {
						if !strings.Contains(stderr, want) {
							t.Errorf("stderr lacks %q: %q", want, stderr)
						}
					}
					assertResourceLimitReadOnly(t, fakes, windows)
				})
			}
		}
	}
}

func assertResourceLimitReadOnly(t *testing.T, fakes *testutil.FakePrograms, windows bool) {
	t.Helper()
	assertResourceLimitCallsReadOnly(t, fakes.Calls("podman"), windows)
	assertNoSSH(t, fakes)
}

func assertResourceLimitCallsReadOnly(t *testing.T, calls []testutil.Call, windows bool) {
	t.Helper()
	preflightCalls := 1
	if windows {
		preflightCalls = resourceWindowsPreflightCalls
	}
	if len(calls) < preflightCalls {
		t.Fatalf("missing preflight calls: %v", calls)
	}
	assertReadOnlyPodmanCalls(t, calls[:preflightCalls])
	for _, call := range calls[preflightCalls:] {
		if len(call.Args) != 3 || (call.Args[0] != "container" && call.Args[0] != "volume") || (call.Args[1] != "exists" && call.Args[1] != "inspect") {
			t.Fatalf("changed sandbox: %v", call)
		}
	}
}

func TestUpReportsEarlierFailuresBeforeResourceConflicts(t *testing.T) {
	for _, host := range resourceLimitHosts {
		windows := host.windows
		for _, object := range []string{"preflight", "container", "workspace", "home", "ssh", "backup", "interrupted-update"} {
			for _, owner := range []string{"", "other"} {
				t.Run(object+"/"+owner+"/"+host.name, func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, windows)
					owned := "default"
					containerOwner := owned
					volumes := map[string]string{"workspace": owned, "home": owned, "ssh": owned}
					var backupOwner *string
					want := "owner conflict"
					switch object {
					case "preflight":
						want = "host prerequisites are missing"
						if windows {
							want = "required prerequisites are missing"
						}
					case "container":
						containerOwner = owner
					case "workspace", "home", "ssh":
						volumes[object] = owner
					case "backup":
						backupOwner = &owner
					case "interrupted-update":
						backupOwner = &owned
						want = "interrupted update"
					}
					responses := upObjectResponses(&containerOwner, false, volumes, backupOwner)
					if object == "preflight" {
						responses[0] = testutil.Response{Stdout: "podman version 3.0.0\n"}
					}
					scriptResourceLimitObjects(t, fakes, windows, responses, map[string]string{"memory": "8589934592"})
					if windows && object == "preflight" {
						preflight := healthyWindowsPodman()
						preflight[0] = testutil.Response{Stdout: "podman version 3.0.0\n"}
						fakes.Script("podman", append(preflight, responses[1:]...)...)
					}
					_, stderr, status := runCLI(t, fixture, "up", "agent01", "--memory", "16g")
					if status == 0 || !strings.Contains(stderr, want) || strings.Contains(stderr, "resource limit") {
						t.Fatalf("status=%d stderr=%q", status, stderr)
					}
					assertResourceLimitReadOnly(t, fakes, windows)
				})
			}
		}
	}

}

func TestUpRefusesUnavailableRecordedResourceLimit(t *testing.T) {
	for _, host := range resourceLimitHosts {
		windows := host.windows
		for _, recorded := range []string{"", "invalid", "0", "-1", "9223372036854775808"} {
			t.Run(recorded+"/"+host.name, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, windows)
				owned := "default"
				responses := upObjectResponses(&owned, false, nil, nil)
				labels := map[string]string{}
				if recorded != "" {
					labels["memory"] = recorded
				}
				scriptResourceLimitObjects(t, fakes, windows, responses, labels)
				_, stderr, status := runCLI(t, fixture, "up", "agent01", "--memory=8g")
				if status == 0 || !strings.Contains(stderr, "--memory") || !strings.Contains(stderr, "remove agent01") || !strings.Contains(stderr, "up agent01") {
					t.Fatalf("status=%d stderr=%q", status, stderr)
				}
				assertResourceLimitReadOnly(t, fakes, windows)
			})
		}
	}

}

func TestUpHelpRecordsResourceFormatsWithoutPodman(t *testing.T) {
	for _, args := range [][]string{{"up", "--help"}, {"up", "agent01", "--help"}, {"up", "agent01", "--memory", "8g", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "windows-arm64", args...)
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, want := range []string{"--memory", "--cpus", "--pids-limit", "--shm-size", "8g", "2048", "1g", "bytes", "three", "remove", "up"} {
				if !strings.Contains(stdout, want) {
					t.Errorf("help lacks %q: %q", want, stdout)
				}
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("help called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

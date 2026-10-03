package controllergroup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
)

func TestHostStateUsesSeparateSafeGroupDirectories(t *testing.T) {
	for _, host := range []string{"linux", "windows"} {
		t.Run(host, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("XDG_STATE_HOME", base)
			t.Setenv("LOCALAPPDATA", base)
			for _, group := range []string{"default", "team-a", "con", "nul"} {
				got, err := controllergroup.StateDirectory(host, group)
				want := filepath.Join(base, "sandboxed-agents", "group-"+group)
				if err != nil || got != want {
					t.Fatalf("path=%q error=%v want=%q", got, err, want)
				}
				if _, err := os.Stat(got); !os.IsNotExist(err) {
					t.Fatalf("resolver changed host state: %v", err)
				}
			}
		})
	}
}

func TestLinuxHostStateDefaultsToHomeForUnsetOrRelativeXDGState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, xdg := range []string{"", "relative/state"} {
		t.Setenv("XDG_STATE_HOME", xdg)
		got, err := controllergroup.StateDirectory("linux", "team-a")
		want := filepath.Join(home, ".local", "state", "sandboxed-agents", "group-team-a")
		if err != nil || got != want {
			t.Fatalf("path=%q error=%v want=%q", got, err, want)
		}
	}
}

func TestStateDirectoryRefusesInvalidGroupAndMissingWindowsLocation(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	for _, group := range []string{"Team", "a.b", "-a", "", "a/b"} {
		if _, err := controllergroup.StateDirectory("linux", group); err == nil {
			t.Fatalf("accepted invalid group %q", group)
		}
	}
	if _, err := controllergroup.StateDirectory("windows", "con"); err == nil {
		t.Fatal("missing Windows state location accepted")
	}
}

func TestWindowsHostStateDoesNotDependOnWorkingDirectory(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "relative/state")
	if _, err := controllergroup.StateDirectory("windows", "team-a"); err == nil {
		t.Fatal("relative Windows state location accepted")
	}
}

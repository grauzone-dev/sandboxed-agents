package controllergroup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const GroupEnvironment = "SANDBOXED_AGENTS_GROUP"

var groupPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func CurrentGroup() (string, error) {
	group, present := os.LookupEnv(GroupEnvironment)
	if !present {
		group = "default"
	}
	if err := ValidateGroup(group); err != nil {
		return "", err
	}
	return group, nil
}

func ValidateGroup(group string) error {
	if !groupPattern.MatchString(group) {
		return fmt.Errorf("invalid controller group %q; %s must match %s", group, GroupEnvironment, groupPattern.String())
	}
	return nil
}

func StateDirectory(hostOS, group string) (string, error) {
	if err := ValidateGroup(group); err != nil {
		return "", err
	}
	var base string
	switch hostOS {
	case "linux":
		base = os.Getenv("XDG_STATE_HOME")
		if !filepath.IsAbs(base) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "state")
		}
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
		if !filepath.IsAbs(base) {
			return "", fmt.Errorf("LOCALAPPDATA must be an absolute path to locate host state")
		}
	default:
		return "", fmt.Errorf("host state is not supported on %s", hostOS)
	}
	return filepath.Join(base, "sandboxed-agents", "group-"+group), nil
}

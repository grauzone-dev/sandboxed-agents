package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func npmProtectedPaths(executable string) ([]string, error) {
	var paths []string
	if value, present := os.LookupEnv("SANDBOXED_AGENTS_NPM_PATHS"); present {
		if err := json.Unmarshal([]byte(value), &paths); err != nil || paths == nil {
			return nil, errors.New(npmPathsInvalid)
		}
		for _, path := range paths {
			if !filepath.IsAbs(path) {
				return nil, errors.New(npmPathsInvalid)
			}
		}
	}
	launcher := filepath.Join(filepath.Dir(executable), "launcher.cjs")
	if _, err := os.Lstat(launcher); err == nil {
		paths = append(paths, launcher)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return paths, nil
}

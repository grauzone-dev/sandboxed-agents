//go:build !windows

package sandbox

import "os"

func hostPathAlias(_ string, info os.FileInfo) (bool, error) {
	return info.Mode()&os.ModeSymlink != 0, nil
}

func validateHostWorkspacePath(string) error { return nil }

func normalizeHostPath(path string) (string, error) { return path, nil }

func checkNestedWorkspaceAliases(string, []string) error { return nil }

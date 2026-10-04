//go:build !windows

package sandbox

import "os"

func hostPathAlias(info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

func validateHostWorkspacePath(string) error { return nil }

func normalizeHostPath(path string) (string, error) { return path, nil }

func checkNestedWorkspaceAliases(string, []string) error { return nil }

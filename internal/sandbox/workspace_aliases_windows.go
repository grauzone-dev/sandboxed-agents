package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func hostPathAlias(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || ok && data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func validateHostWorkspacePath(path string) error {
	_, err := windowsBindSource(path)
	return err
}

func checkNestedWorkspaceAliases(workspace string, protectedPaths []string) error {
	roots := []string{workspace}
	visited := make(map[string]bool)
	for len(roots) > 0 {
		root := roots[0]
		roots = roots[1:]
		if visited[root] {
			continue
		}
		visited[root] = true
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&(os.ModeSymlink|os.ModeIrregular) == 0 {
				return nil
			}
			alias, err := entry.Info()
			if err != nil {
				return err
			}
			if !hostPathAlias(alias) {
				return nil
			}
			target, err := resolveHostPath(path)
			if err != nil {
				return err
			}
			for _, protected := range protectedPaths {
				overlap, err := hostPathsOverlap(target, protected)
				if err != nil {
					return err
				}
				if overlap {
					return fmt.Errorf(workspaceProtectedError, workspace, protected)
				}
			}
			info, err := os.Stat(target)
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if info.IsDir() {
				roots = append(roots, target)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf(workspaceAliasError, root, err)
		}
	}
	return nil
}

func normalizeHostPath(path string) (string, error) {
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) || filepath.Dir(path) == path {
			return path, err
		}
		suffix = append(suffix, filepath.Base(path))
		path = filepath.Dir(path)
	}
}

func fileHasAliases(path string, info os.FileInfo) (bool, error) {
	if info.IsDir() {
		return false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &data); err != nil {
		return false, err
	}
	return data.NumberOfLinks > 1, nil
}

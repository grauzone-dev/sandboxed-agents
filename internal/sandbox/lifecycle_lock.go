package sandbox

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
)

func LockLifecycle(hostOS, group, name string) (func(), error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	state, err := controllergroup.StateDirectory(hostOS, group)
	if err != nil {
		return nil, err
	}
	locks := filepath.Join(state, "locks")
	if err := os.MkdirAll(locks, 0700); err != nil {
		return nil, fmt.Errorf(lifecycleLockFailureFormat, name, err)
	}
	hash := sha256.Sum256([]byte(containerPrefix + group + "." + name))
	path := filepath.Join(locks, fmt.Sprintf("%x", hash))
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf(lifecycleLockObjectFormat, path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf(lifecycleLockFailureFormat, name, err)
	}
	file, err := openLifecycleLock(path)
	if err != nil {
		return nil, fmt.Errorf(lifecycleLockFailureFormat, name, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf(lifecycleLockFailureFormat, name, err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf(lifecycleLockObjectFormat, path)
	}
	unlock, err := acquireLifecycleLock(file)
	if err != nil {
		file.Close()
		if lifecycleLockBusy(err) {
			return nil, fmt.Errorf(lifecycleBusyFormat, name)
		}
		return nil, fmt.Errorf(lifecycleLockFailureFormat, name, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlock()
			file.Close()
		})
	}, nil
}

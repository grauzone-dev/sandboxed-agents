package filelock_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/filelock"
)

func TestFileLockExcludesAnotherOpenFileAndAllowsAcquisitionAfterUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	unlock, err := filelock.TryLock(first)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if unexpectedUnlock, err := filelock.TryLock(second); !filelock.IsBusy(err) || unexpectedUnlock != nil {
		if unexpectedUnlock != nil {
			unexpectedUnlock()
		}
		t.Fatalf("contending lock error=%v unlock present=%v", err, unexpectedUnlock != nil)
	}
	unlock()
	secondUnlock, err := filelock.TryLock(second)
	if err != nil {
		t.Fatalf("released file remains locked: %v", err)
	}
	secondUnlock()
	if _, err := first.Stat(); err != nil {
		t.Fatalf("unlock closed the caller's file: %v", err)
	}
}

func TestFileLockReportsAnInvalidFileAsAnErrorInsteadOfContention(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	unlock, err := filelock.TryLock(file)
	if err == nil || filelock.IsBusy(err) || filelock.IsInterrupted(err) || unlock != nil {
		t.Fatalf("closed file error=%v unlock present=%v", err, unlock != nil)
	}
	if filelock.IsBusy(nil) || filelock.IsInterrupted(nil) {
		t.Fatal("successful acquisition classified as retryable")
	}
}

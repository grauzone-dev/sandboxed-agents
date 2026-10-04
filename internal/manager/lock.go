package manager

import (
	"context"
	"os"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/filelock"
)

func lockManager(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		unlock, err := filelock.TryLock(file)
		if err == nil {
			return func() { unlock(); file.Close() }, nil
		}
		if !filelock.IsBusy(err) && !filelock.IsInterrupted(err) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

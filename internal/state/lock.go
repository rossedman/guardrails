package state

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// staleLock is how old a lock file must be before it is presumed abandoned
// by a crashed process. Hooks are bounded by their own timeouts, so nothing
// legitimate holds the lock this long.
const staleLock = 2 * time.Minute

// lock acquires an exclusive lock file using O_EXCL, which works the same on
// every platform Claude Code runs on.
func lock(path string, timeout time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLock {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for lock %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

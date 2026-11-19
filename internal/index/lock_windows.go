//go:build windows

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const lockFile = "index.lock"

// Lock represents an exclusive lock on the index.
// Note: Windows locking is not yet implemented. This uses a simple PID-based check.
type Lock struct {
	path string
	file *os.File
}

// AcquireLock attempts to acquire an exclusive lock on the index.
// On Windows, this uses a simple file-based check (not truly atomic).
func AcquireLock(ckbDir string) (*Lock, error) {
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		return nil, fmt.Errorf("creating .ckb directory: %w", err)
	}

	path := filepath.Join(ckbDir, lockFile)

	// Check if lock file exists and contains a running PID
	if content, err := os.ReadFile(path); err == nil && len(content) > 0 {
		// Lock file exists - on Windows we can't do proper flock,
		// so just warn and proceed (best effort)
		// In v7.3 we should implement proper Windows file locking
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if _, err := file.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		file.Close()
		return nil, fmt.Errorf("writing PID to lock file: %w", err)
	}

	return &Lock{path: path, file: file}, nil
}

// Release releases the lock and removes the lock file.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}

	l.file.Close()
	os.Remove(l.path)
}

// BuildRunning can't tell on Windows: nothing locks the file there, and one left by a
// crash looks the same as one in use, so it doesn't claim a build it can't see.
func BuildRunning(ckbDir string) bool {
	return false
}

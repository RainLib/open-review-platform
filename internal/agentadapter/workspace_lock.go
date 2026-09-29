package agentadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// WorkspaceLock serializes adapters that share one checkout volume, even if
// they were misconfigured with different receipt directories. It is a
// single-host safety fence, not a distributed execution lease.
type WorkspaceLock struct {
	file *os.File
	root string
}

func LockWorkspaceRoot(root string) (*WorkspaceLock, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("adapter workspace root must be absolute")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("adapter workspace root must be an exact directory")
	}
	file, err := os.OpenFile(filepath.Join(root, ".adapter-workspace.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open adapter workspace lock: %w", err)
	}
	lockInfo, err := file.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm()&0o077 != 0 {
		_ = file.Close()
		return nil, fmt.Errorf("adapter workspace lock must be a private regular file")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another adapter owns the workspace volume: %w", err)
	}
	return &WorkspaceLock{file: file, root: filepath.Clean(root)}, nil
}

func (lock *WorkspaceLock) owns(root string) bool {
	return lock != nil && lock.file != nil && lock.root == filepath.Clean(root)
}

func (lock *WorkspaceLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

package agentadapter

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const maxExecutorOwnershipEntries = 100000

func configureExecutorIdentity(command *exec.Cmd, identity executorIdentity) error {
	if os.Geteuid() != 0 || command.SysProcAttr == nil {
		return fmt.Errorf("isolated coding executor requires a privileged adapter process")
	}
	// Explicitly clear supplementary groups; inheriting the adapter's root
	// group would invalidate the file-permission boundary.
	command.SysProcAttr.Credential = &syscall.Credential{Uid: identity.uid, Gid: identity.gid, Groups: []uint32{}}
	return nil
}

// Transfer the checkout to the unprivileged CLI, then reclaim it before any
// trusted Git or provider operation. Lchown deliberately never follows a
// symlink supplied by the checkout or by the coding CLI.
func transferExecutorWorkspace(root string, identity executorIdentity) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("workspace ownership transfer requires a privileged adapter process")
	}
	paths := make([]string, 0, 1024)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		paths = append(paths, path)
		if len(paths) > maxExecutorOwnershipEntries {
			return fmt.Errorf("executor workspace exceeds ownership entry budget")
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Keep the checkout root private to the adapter until every child entry is
	// owned by the CLI. The root changes ownership last.
	for index := len(paths) - 1; index >= 0; index-- {
		if err := os.Lchown(paths[index], int(identity.uid), int(identity.gid)); err != nil {
			return fmt.Errorf("transfer executor workspace ownership: %w", err)
		}
	}
	return nil
}

func reclaimExecutorWorkspace(root string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("workspace ownership recovery requires a privileged adapter process")
	}
	// Reclaim the root first, so an escaped child cannot open new checkout
	// paths while the adapter validates and publishes the result.
	if err := os.Lchown(root, 0, 0); err != nil {
		return err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		return os.Lchown(path, 0, 0)
	})
}

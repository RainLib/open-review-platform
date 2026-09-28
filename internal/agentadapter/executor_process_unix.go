//go:build linux || darwin

package agentadapter

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureExecutorProcessGroup(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return stopExecutorProcessGroup(command)
	}
	// A descendant that inherits stdout/stderr must not hold Cmd.Run
	// open forever after the leading CLI exits or is cancelled.
	command.WaitDelay = 2 * time.Second
	return nil
}

func stopExecutorProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil || command.Process.Pid < 1 {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

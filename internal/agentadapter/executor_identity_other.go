//go:build !linux

package agentadapter

import (
	"fmt"
	"os/exec"
)

func configureExecutorIdentity(_ *exec.Cmd, _ executorIdentity) error {
	return fmt.Errorf("isolated coding executor identities are only supported on Linux")
}

func transferExecutorWorkspace(_ string, _ executorIdentity) error {
	return fmt.Errorf("isolated coding executor identities are only supported on Linux")
}

func reclaimExecutorWorkspace(_ string) error {
	return fmt.Errorf("isolated coding executor identities are only supported on Linux")
}

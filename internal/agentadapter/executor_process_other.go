//go:build !linux && !darwin

package agentadapter

import (
	"fmt"
	"os/exec"
)

// Do not silently run an uncontained coding CLI on a platform for which the
// adapter has no process-tree cancellation contract.
func configureExecutorProcessGroup(_ *exec.Cmd) error {
	return fmt.Errorf("coding executor process groups are unsupported on this platform")
}

func stopExecutorProcessGroup(_ *exec.Cmd) error { return nil }

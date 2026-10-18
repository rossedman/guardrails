//go:build !unix

package tools

import (
	"os"
	"os/exec"
)

// spawnDetached starts `guardrails tools install <names>` in the background.
func spawnDetached(names ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, append([]string{"tools", "install", "--quiet"}, names...)...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

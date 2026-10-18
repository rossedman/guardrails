//go:build unix

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached starts `guardrails tools install <names>` in its own
// session so it outlives the short-lived hook process.
func spawnDetached(names ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, append([]string{"tools", "install", "--quiet"}, names...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

package update

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// detach puts the update in its own session: it stops and restarts this
// agent, and must outlive it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func processAlive(pid int) bool {
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return err == nil
}

//go:build !linux

package update

import "os/exec"

// The agent runs on Linux; elsewhere (development) there's no session to
// leave and no /proc to look into.
func detach(*exec.Cmd) {}

func processAlive(int) bool { return true }

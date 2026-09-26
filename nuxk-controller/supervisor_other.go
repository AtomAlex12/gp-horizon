//go:build !linux

package main

import (
	"context"
	"errors"
)

// The plugin host needs Linux (setpriv, process groups, capabilities); on
// other systems `serve` runs alone, without plugins.
func runSupervisor(context.Context) error {
	return errors.New("supervise: plugins run only on Linux (the Pi's container)")
}

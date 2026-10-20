//go:build !linux

package probe

import (
	"errors"
	"syscall"
)

// bindControl: binding a socket to an interface is Linux-only; on a dev
// machine the tunnel paths simply report an error.
func bindControl(iface string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		return errors.New("binding to an interface is supported on Linux only")
	}
}

// markControl: firewall marks are Linux-only, and there is no nfqws2 to skip
// elsewhere; sockets go out unmarked.
func markControl(uint32) func(network, address string, c syscall.RawConn) error { return nil }

//go:build linux

package probe

import (
	"syscall"
)

// bindControl pins a socket to a network interface (what `curl --interface`
// does), so a probe leaves through a tunnel instead of the default route.
func bindControl(iface string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		}); err != nil {
			return err
		}
		return serr
	}
}

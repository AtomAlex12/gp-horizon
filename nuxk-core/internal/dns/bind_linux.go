package dns

import "syscall"

// bindTo sends a socket out of iface (SO_BINDTODEVICE), whatever the routing
// table says — the way S52xray-nuxk's probe uses curl --interface: packets
// enter the tunnel's TUN and the engine carries them.
func bindTo(iface string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		}); err != nil {
			return err
		}
		return serr
	}
}

const canBind = true

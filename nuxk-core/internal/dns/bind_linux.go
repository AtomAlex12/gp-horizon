package dns

import (
	"os"
	"syscall"
)

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

// inode: a file's inode — a rotated log is a new file under the same name.
func inode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

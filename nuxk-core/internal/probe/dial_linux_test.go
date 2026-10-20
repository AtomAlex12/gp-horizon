//go:build linux

package probe

import (
	"context"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

// Direct sockets carry nfqws2's mark, desync ones don't; and a mark that
// can't be set (not root) must not stop the probe.
func TestDirectMarkedDesyncNot(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := &Prober{Timeout: time.Second}
	mark := func(via Via) int {
		c, err := p.dial(context.Background(), via, ln.Addr().String())
		if err != nil {
			t.Fatalf("%s: %v", via, err)
		}
		defer c.Close()
		raw, err := c.(*net.TCPConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var m int
		_ = raw.Control(func(fd uintptr) { m, _ = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK) })
		return m
	}
	direct, desync := mark(ViaDirect), mark(ViaDesync)
	if desync != 0 {
		t.Errorf("desync must go out unmarked, mark=%#x", desync)
	}
	if os.Geteuid() != 0 {
		t.Skip("not root: the mark can't be set here; the dial still worked")
	}
	if direct != 0x40000000 {
		t.Errorf("direct mark=%#x, want 0x40000000", direct)
	}
}

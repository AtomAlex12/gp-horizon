//go:build linux

package probe

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"
)

// Direct sockets carry nfqws2's mark. Desync sockets are unmarked.
func TestDirectMarkedDesyncNot(t *testing.T) {
	if err := markAvailable(0x40000000); err != nil {
		t.Skipf("requires permission to mark a socket: %v", err)
	}
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
	if direct != 0x40000000 {
		t.Errorf("direct mark=%#x, want 0x40000000", direct)
	}
}

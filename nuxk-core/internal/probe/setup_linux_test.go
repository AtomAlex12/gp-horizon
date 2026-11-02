//go:build linux

package probe

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
)

type brokenRawConn struct{ err error }

func (c brokenRawConn) Control(f func(uintptr)) error {
	if c.err != nil {
		return c.err
	}
	f(^uintptr(0)) // invalid descriptor: a deterministic setsockopt failure
	return nil
}
func (brokenRawConn) Read(func(uintptr) bool) error  { return nil }
func (brokenRawConn) Write(func(uintptr) bool) error { return nil }

func TestSocketSetupFailuresAreReported(t *testing.T) {
	for _, operation := range []struct {
		name string
		fn   func(string, string, syscall.RawConn) error
	}{{"mark", markControl(0x40000000)}, {"interface", bindControl("missing")}} {
		for _, failure := range []error{syscall.EPERM, nil} {
			want := failure
			if want == nil {
				want = syscall.EBADF
			}
			err := operation.fn("tcp4", "example.com:443", brokenRawConn{err: failure})
			if !errors.Is(err, want) {
				t.Errorf("%s setup error=%v, want %v", operation.name, err, want)
			}
		}
	}
}

func TestFailedSocketSetupSkipsMeasurement(t *testing.T) {
	for _, control := range []func(string, string, syscall.RawConn) error{markControl(1), bindControl("missing")} {
		err := control("tcp4", "example.com:443", brokenRawConn{err: syscall.EPERM})
		if err == nil {
			t.Error("socket setup failure disappeared")
			continue
		}
		p := &Prober{Samples: 1, NoFreeze: true, Avail: func(Via) string { return "" },
			Dial: func(context.Context, Via, string) (net.Conn, error) { return nil, err }}
		r := p.Way(context.Background(), "example.com", net.ParseIP("203.0.113.1"), ViaDirect)
		if r.Skipped == "" || r.Tries != 0 || r.Clean() || r.TCPFail != FailNone {
			t.Errorf("local setup failure became a network measurement: %+v", r)
		}
	}
}

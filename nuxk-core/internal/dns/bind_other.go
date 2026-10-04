//go:build !linux

package dns

import (
	"errors"
	"os"
	"syscall"
)

// Binding to an interface is Linux's (the router); elsewhere (development)
// only the direct path works.
func bindTo(string) func(network, address string, c syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error {
		return errors.New("binding to an interface needs Linux")
	}
}

const canBind = false

// inode: unknown here; a rotated log is caught by its size shrinking.
func inode(os.FileInfo) uint64 { return 0 }

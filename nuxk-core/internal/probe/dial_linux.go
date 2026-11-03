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
			return setupFailure("не удалось привязать сокет к интерфейсу", err)
		}
		return setupFailure("не удалось привязать сокет к интерфейсу", serr)
	}
}

// markControl requires the bypass mark before opening a direct connection.
func markControl(mark uint32) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var markErr error
		if err := c.Control(func(fd uintptr) {
			markErr = setMark(int(fd), mark)
		}); err != nil {
			return setupFailure("не удалось получить доступ к сокету", err)
		}
		return markErr
	}
}

func setMark(fd int, mark uint32) error {
	return setupFailure("не удалось установить метку сокета",
		syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_MARK, int(mark)))
}

func markAvailable(mark uint32) error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return setupFailure("не удалось создать пробный сокет", err)
	}
	defer syscall.Close(fd)
	return setMark(fd, mark)
}

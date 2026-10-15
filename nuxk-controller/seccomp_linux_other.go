//go:build linux && !arm64 && !amd64

package main

import "errors"

// The controller ships for arm64 and amd64; elsewhere plugins start without
// the uid filter (seccomp_linux.go).
const idFilterSupported = false

func pluginExec([]string) error {
	return errors.New("plugin-exec: the uid filter is built for arm64 and amd64 only")
}

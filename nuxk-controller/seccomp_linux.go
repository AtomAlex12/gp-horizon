//go:build linux && (arm64 || amd64)

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

// A plugin may switch users — zapret2 drops nfqws2 to 1:3003, which takes
// SETUID/SETGID — and with them it could become the web process's user
// (serveUID), the owner of controller.json and of the supervisor's socket:
// "root without CAP_DAC_*" alone doesn't stop that. So the supervisor starts
// every plugin through `nuxk-controller plugin-exec`, which sets a seccomp
// filter refusing any switch to that uid or gid, and only then becomes
// setpriv, then the plugin: the filter goes with every process they start.

const idFilterSupported = true

// classic BPF, as seccomp takes it
type sockFilter struct {
	code   uint16
	jt, jf uint8
	k      uint32
}

type sockFprog struct {
	len    uint16
	filter *sockFilter
}

const (
	bpfLdWAbs = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJeqK   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfJgeK   = 0x35 // BPF_JMP | BPF_JGE | BPF_K
	bpfRetK   = 0x06 // BPF_RET | BPF_K

	retAllow = 0x7fff0000 // SECCOMP_RET_ALLOW
	retErrno = 0x00050000 // SECCOMP_RET_ERRNO

	prSetNoNewPrivs   = 38
	seccompModeFilter = 1 // SECCOMP_SET_MODE_FILTER
	seccompTsync      = 1 // SECCOMP_FILTER_FLAG_TSYNC

	// struct seccomp_data
	dataNr   = 0
	dataArch = 4
	dataArgs = 16
)

// idSyscalls: every call that sets a uid or gid, and how many ids it takes.
var idSyscalls = []struct {
	nr   uintptr
	args int
}{
	{syscall.SYS_SETUID, 1}, {syscall.SYS_SETGID, 1},
	{syscall.SYS_SETREUID, 2}, {syscall.SYS_SETREGID, 2},
	{syscall.SYS_SETRESUID, 3}, {syscall.SYS_SETRESGID, 3},
	{syscall.SYS_SETFSUID, 1}, {syscall.SYS_SETFSGID, 1},
}

// idFilter: a program refusing (EPERM) any of idSyscalls with id among its
// ids, and any call of another ABI (32-bit, x32 — other numbers, other
// argument sizes); everything else is allowed. uid_t and gid_t are 32 bits:
// only the low word of an argument counts, as for the kernel.
func idFilter(id uint32) []sockFilter {
	deny := uint32(retErrno | uint32(syscall.EPERM))
	p := []sockFilter{
		{bpfLdWAbs, 0, 0, dataArch},
		{bpfJeqK, 1, 0, auditArch},
		{bpfRetK, 0, 0, deny},
		{bpfLdWAbs, 0, 0, dataNr},
	}
	if x32Bit != 0 {
		p = append(p, sockFilter{bpfJgeK, 0, 1, x32Bit}, sockFilter{bpfRetK, 0, 0, deny})
	}
	for _, s := range idSyscalls {
		// the call's block: per argument load, compare, refuse; then allow
		p = append(p, sockFilter{bpfJeqK, 0, uint8(3*s.args + 1), uint32(s.nr)})
		for i := 0; i < s.args; i++ {
			p = append(p,
				sockFilter{bpfLdWAbs, 0, 0, uint32(dataArgs + 8*i)}, // little-endian: the low word first
				sockFilter{bpfJeqK, 0, 1, id},
				sockFilter{bpfRetK, 0, 0, deny})
		}
		p = append(p, sockFilter{bpfRetK, 0, 0, retAllow})
	}
	return append(p, sockFilter{bpfRetK, 0, 0, retAllow})
}

// installIDFilter sets the filter on every thread of this process (and so on
// whatever it execs); no_new_privs first, as seccomp requires without
// CAP_SYS_ADMIN — setpriv sets it for the plugin anyway.
func installIDFilter(id uint32) error {
	f := idFilter(id)
	prog := sockFprog{len: uint16(len(f)), filter: &f[0]}
	if _, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("no_new_privs: %w", e)
	}
	r, _, e := syscall.RawSyscall(sysSeccomp, seccompModeFilter, seccompTsync, uintptr(unsafe.Pointer(&prog)))
	runtime.KeepAlive(f)
	if e != 0 {
		return fmt.Errorf("seccomp: %w", e)
	}
	if r != 0 {
		return fmt.Errorf("seccomp: thread %d did not take the filter", r)
	}
	return nil
}

// pluginExec is `nuxk-controller plugin-exec -- argv…`: the filter, then
// argv in place of this process.
func pluginExec(args []string) error {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return errors.New("plugin-exec: nothing to run")
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	if err := installIDFilter(serveUID); err != nil {
		return err
	}
	return syscall.Exec(path, args, os.Environ())
}

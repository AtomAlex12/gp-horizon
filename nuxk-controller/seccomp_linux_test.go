//go:build linux && (arm64 || amd64)

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// The filter, in a child process (it can't be taken off): as root, every way
// to become serveUID is refused — high bits set in the argument too — and
// the switch zapret2 makes for nfqws2 (1:3003) still works. Needs root: an
// ordinary user can't switch at all, filter or not.
func TestIDFilter(t *testing.T) {
	if os.Getenv("NUXK_IDFILTER_CHILD") == "1" {
		idFilterChild()
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root (run in a container as root)")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestIDFilter$")
	cmd.Env = append(os.Environ(), "NUXK_IDFILTER_CHILD=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "idfilter ok") {
		t.Fatalf("child:\n%s", out)
	}
}

func idFilterChild() {
	runtime.LockOSThread()
	if err := installIDFilter(serveUID); err != nil {
		fmt.Println("install:", err)
		os.Exit(1)
	}
	none := ^uintptr(0) // -1: "leave as it is"
	id := uintptr(serveUID)
	refused := []struct {
		what string
		nr   uintptr
		a    [3]uintptr
	}{
		{"setgid", syscall.SYS_SETGID, [3]uintptr{id}},
		{"setuid", syscall.SYS_SETUID, [3]uintptr{id}},
		{"setreuid", syscall.SYS_SETREUID, [3]uintptr{none, id}},
		{"setresuid", syscall.SYS_SETRESUID, [3]uintptr{none, none, id}},
		{"setresgid", syscall.SYS_SETRESGID, [3]uintptr{id, none, none}},
		{"setfsuid", syscall.SYS_SETFSUID, [3]uintptr{id}},
		{"setfsgid", syscall.SYS_SETFSGID, [3]uintptr{id}},
		{"setuid, high bits", syscall.SYS_SETUID, [3]uintptr{id | 1<<32}},
	}
	bad := ""
	for _, c := range refused {
		if _, _, e := syscall.RawSyscall(c.nr, c.a[0], c.a[1], c.a[2]); e != syscall.EPERM {
			bad += fmt.Sprintf("%s: %v (want EPERM); ", c.what, e)
		}
	}
	// what nfqws2 does under zapret2's blockcheck2: gid 3003, uid 1
	if _, _, e := syscall.RawSyscall(syscall.SYS_SETGID, 3003, 0, 0); e != 0 {
		bad += fmt.Sprintf("setgid 3003: %v; ", e)
	}
	if _, _, e := syscall.RawSyscall(syscall.SYS_SETUID, 1, 0, 0); e != 0 {
		bad += fmt.Sprintf("setuid 1: %v; ", e)
	}
	if bad != "" {
		fmt.Println(bad)
		os.Exit(1)
	}
	fmt.Println("idfilter ok")
	os.Exit(0)
}

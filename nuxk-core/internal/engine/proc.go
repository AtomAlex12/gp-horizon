package engine

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcRoot is where /proc lives (tests point it at a fake tree).
var ProcRoot = "/proc"

// userHZ is the kernel's USER_HZ — the unit of /proc/<pid>/stat times. It is
// 100 on every Linux ABI nuxk runs on (arm64, mips, mipsel, x86-64).
const userHZ = 100

// ProcUptime returns how long process pid has been running, from its start
// time in /proc/<pid>/stat against the box's /proc/uptime. 0 = unknown.
//
// The init scripts report uptime from their pidfile's mtime via `stat`,
// which KeeneticOS's busybox may lack — this reads the kernel instead, with
// no fork.
func ProcUptime(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	stat, err := os.ReadFile(filepath.Join(ProcRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	// "pid (comm) state ppid ..." — comm may hold spaces, so split after ')'.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 { // starttime is field 22 overall, 20th after comm
		return 0
	}
	start, err := strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return 0
	}
	upf := strings.Fields(readString(filepath.Join(ProcRoot, "uptime")))
	if len(upf) == 0 {
		return 0
	}
	up, err := strconv.ParseFloat(upf[0], 64)
	if err != nil {
		return 0
	}
	if sec := int64(up) - int64(start/userHZ); sec > 0 {
		return sec
	}
	return 0
}

func readString(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

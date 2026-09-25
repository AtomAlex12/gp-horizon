package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, p, body string) {
	t.Helper()
	full := filepath.Join(root, p)
	os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsFromProc(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/proc/net/dev", `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  100 1 0 0 0 0 0 0  100 1 0 0 0 0 0 0
  ppp0: 5000 10 0 0 0 0 0 0 7000 12 0 0 0 0 0 0
opkgtun0: 300 3 0 0 0 0 0 0 400 4 0 0 0 0 0 0
`)
	write(t, root, "/proc/net/route", "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n"+
		"br0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"+
		"eth3\t00000000\t0101A8C0\t0003\t0\t0\t10\t00000000\t0\t0\t0\n"+
		"ppp0\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n")
	write(t, root, "/proc/net/netfilter/nfnetlink_queue", "  300  1234     0 2 65531     5     1     98765  1\n")
	write(t, root, "/proc/sys/net/netfilter/nf_conntrack_count", "412\n")
	write(t, root, "/proc/sys/net/netfilter/nf_conntrack_max", "32768\n")
	write(t, root, "/proc/loadavg", "0.42 0.30 0.20 1/100 999\n")
	write(t, root, "/proc/meminfo", "MemTotal:  524288 kB\nMemAvailable:  300000 kB\n")
	n := &Node{Root: root}
	m := n.Metrics()
	if m.WAN != "ppp0" || m.Ifaces["ppp0"] != (Iface{Rx: 5000, Tx: 7000}) || m.Ifaces["opkgtun0"].Tx != 400 {
		t.Fatalf("ifaces: wan=%q %+v", m.WAN, m.Ifaces)
	}
	if _, ok := m.Ifaces["lo"]; ok {
		t.Error("lo reported")
	}
	if len(m.NFQueues) != 1 || m.NFQueues[0] != (Queue{Num: 300, Packets: 98765, Dropped: 6, Waiting: 0}) {
		t.Errorf("nfqueues = %+v", m.NFQueues)
	}
	if m.Conntrack != 412 || m.ConnMax != 32768 || m.Load1 != 0.42 || m.MemAvail != 300000 {
		t.Errorf("metrics = %+v", m)
	}
}

func TestRole(t *testing.T) {
	root := t.TempDir()
	n := &Node{Root: root}
	if n.Info(context.Background()).Role != RoleHost {
		t.Error("no ndmc must be host")
	}
	write(t, root, "/bin/ndmc", "")
	if n.role() != RoleRouter {
		t.Error("ndmc present must be router")
	}
	n.RoleCfg = "stand"
	if n.role() != RoleStand {
		t.Error("NODE_ROLE wins")
	}
}

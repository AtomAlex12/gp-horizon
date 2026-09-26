package main

import (
	"bufio"
	"strconv"
	"strings"
	"time"
)

// Report is what detect.sh found on the router, parsed.
type Report struct {
	Raw map[string]string `json:"-"`

	Arch       string `json:"arch"`     // nuxk-core payload suffix; "" = unsupported
	ArchRaw    string `json:"arch_raw"` // what the router calls itself
	Entware    bool   `json:"entware"`
	KeeneticOS string `json:"keenetic_os,omitempty"`
	Model      string `json:"model,omitempty"`
	OptFreeKB  int    `json:"opt_free_kb"`
	MemAvailKB int    `json:"mem_avail_kb"`
	ClockSkew  int64  `json:"clock_skew_s"` // router clock minus ours
	LANIP      string `json:"lan_ip,omitempty"`

	Pkgs       map[string]string `json:"pkgs"` // name -> version, "" = absent
	NfqwsFeed  bool              `json:"nfqws_feed"`
	Kmods      map[string]string `json:"kmods"` // loaded | available | missing
	Init       map[string]bool   `json:"init"`
	UsqueReady bool              `json:"usque_contract"`
	UsqueIface string            `json:"usque_iface,omitempty"` // ndm name, e.g. OpkgTun0

	XrayVersion string            `json:"xray_version,omitempty"` // /opt/sbin/xray, "" = none
	XrayReady   bool              `json:"xray_ready"`             // xray + S52xray-nuxk
	XrayIface   string            `json:"xray_iface,omitempty"`   // set by the xray step
	NdmTuns     map[string]string `json:"ndm_tuns,omitempty"`     // OpkgTunN → description ("-" = none)

	NuxkCore    string `json:"nuxk_core,omitempty"` // installed version, "" = none
	NuxkConf    bool   `json:"nuxk_conf"`
	NuxkListen  string `json:"nuxk_listen,omitempty"`
	NuxkRunning bool   `json:"nuxk_running"`
}

// ParseReport turns detect.sh output into a Report. at is when it ran (for
// clock skew — a router with a wrong clock can't do HTTPS to package feeds).
func ParseReport(out string, at time.Time) Report {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(strings.TrimRight(sc.Text(), "\r"), " ")
		if k != "" {
			kv[k] = strings.TrimSpace(v)
		}
	}
	r := Report{
		Raw:        kv,
		ArchRaw:    firstNonEmpty(kv["opkg_arch"], kv["uname_m"]),
		Entware:    kv["opkg"] == "1",
		KeeneticOS: kv["keenetic_os"],
		Model:      kv["keenetic_model"],
		OptFreeKB:  atoi(kv["opt_free_kb"]),
		MemAvailKB: atoi(kv["mem_avail_kb"]),
		LANIP:      kv["lan_ip"],
		Pkgs:       map[string]string{},
		NfqwsFeed:  kv["feed.nfqws2"] == "1",
		Kmods:      map[string]string{},
		Init:       map[string]bool{},
		NdmTuns:    map[string]string{},
		UsqueReady: kv["usque_contract"] == "1",
		UsqueIface: ndmName(kv["usque_iface"]),
		NuxkCore:   kv["nuxk_core"],
		NuxkConf:   kv["nuxk_conf"] == "1",
		NuxkListen: kv["nuxk_listen"],

		NuxkRunning: kv["nuxk_running"] == "1",
	}
	r.Arch = archFor(kv["opkg_arch"], kv["uname_m"], kv["mips_endian"])
	if c := atoi(kv["clock"]); c > 0 {
		r.ClockSkew = int64(c) - at.Unix()
	}
	for k, v := range kv {
		switch {
		case strings.HasPrefix(k, "pkg."):
			r.Pkgs[strings.TrimPrefix(k, "pkg.")] = v
		case strings.HasPrefix(k, "kmod."):
			r.Kmods[strings.TrimPrefix(k, "kmod.")] = v
		case strings.HasPrefix(k, "init."):
			r.Init[strings.TrimPrefix(k, "init.")] = v == "1"
		case strings.HasPrefix(k, "ndm_tun."):
			r.NdmTuns[strings.TrimPrefix(k, "ndm_tun.")] = v
		}
	}
	r.XrayVersion = kv["xray_version"]
	r.XrayReady = r.XrayVersion != "" && r.Init["S52xray-nuxk"]
	return r
}

// archFor maps the router's package arch to a nuxk-core build. Package arch is
// preferred: `uname -m` says "mips" on little-endian MT7621 Keenetics too.
func archFor(opkgArch, unameM, endian string) string {
	a := strings.ToLower(firstNonEmpty(opkgArch, unameM))
	switch {
	case strings.Contains(a, "aarch64"), strings.Contains(a, "arm64"):
		return "aarch64"
	case strings.Contains(a, "mips64"):
		return ""
	case strings.Contains(a, "mipsel"), strings.Contains(a, "mipsle"):
		return "mipsel"
	case a == "mips":
		switch endian {
		case "1":
			return "mipsel"
		case "2":
			return "mips"
		}
		return ""
	case strings.Contains(a, "mips"):
		return "mips"
	case strings.Contains(a, "x86_64"), strings.Contains(a, "amd64"), strings.Contains(a, "x64"):
		return "x86_64"
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// ndmName turns a Linux tun name into the Keenetic interface name the way
// usque-keenetic does: opkgtun0 → OpkgTun0.
func ndmName(iface string) string {
	if !strings.HasPrefix(iface, "opkgtun") {
		return iface
	}
	return "OpkgTun" + strings.TrimPrefix(iface, "opkgtun")
}

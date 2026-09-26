package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// xray for the router: the official XTLS release, pinned by version and by
// the SHA-256 of each archive (as its .dgst lists it). The installer
// downloads it on its own machine, checks it, and puts only the binary on
// the router — not geoip/geosite (≈30 MB the config doesn't use). MIPS
// routers get the soft-float build: most have no FPU (nuxk-core is built
// the same way).
const xrayVersion = "26.3.27"

type xrayAsset struct{ zip, bin, sha256 string }

var xrayAssets = map[string]xrayAsset{
	"aarch64": {"Xray-linux-arm64-v8a.zip", "xray", "4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c"},
	"mips":    {"Xray-linux-mips32.zip", "xray_softfloat", "a562f2edbdabc0f1a17eee2226fa9f710e6b61c2d5e2bf3435157b5ce40b1c67"},
	"mipsel":  {"Xray-linux-mips32le.zip", "xray_softfloat", "fe1ded07a64fe0a406c6c1089f09b6c2999fc2309509ca4c98d93469c0cbf9df"},
	"x86_64":  {"Xray-linux-64.zip", "xray", "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae"},
}

// xrayBaseURL is where the releases live (tests point it at a fake).
var xrayBaseURL = "https://github.com/XTLS/Xray-core/releases/download"

const (
	pXray     = "/opt/sbin/xray"
	pXrayInit = "/opt/etc/init.d/S52xray-nuxk"

	xrayMark   = "nuxk-vless" // description of the OpkgTun nuxk creates
	xrayNeedKB = 45 * 1024    // the binary (≈34–38 MB) and headroom
	xrayMemKB  = 64 * 1024    // below this, say it may not fit in memory
	xrayMaxBin = 80 << 20     // read limit for the binary in the archive
	xrayMaxZip = 64 << 20     // and for the archive
	xrayFetch  = 5 * time.Minute
)

// xrayIface is the OpkgTun for VLESS: the one nuxk made before (its
// description is nuxk-vless), else the first free one from OpkgTun1 —
// never someone else's, never usque's. "" = none free.
func xrayIface(r Report) string {
	var ours []string
	for name, d := range r.NdmTuns {
		if d == xrayMark {
			ours = append(ours, name)
		}
	}
	if len(ours) > 0 {
		sort.Strings(ours)
		return ours[0]
	}
	for n := 1; n < 10; n++ {
		name := fmt.Sprintf("OpkgTun%d", n)
		if _, taken := r.NdmTuns[name]; !taken && name != r.UsqueIface {
			return name
		}
	}
	return ""
}

// planXray is the checklist line for VLESS. Like WARP it is opt-in: it
// creates a KeeneticOS interface and saves the router's config, so ticking
// it is the consent.
func planXray(r Report) Item {
	it := Item{ID: "xray", Title: "xray (VLESS)"}
	tun := xrayIface(r)
	a, ok := xrayAssets[r.Arch]
	mem := ""
	if r.MemAvailKB > 0 && r.MemAvailKB < xrayMemKB {
		mem = fmt.Sprintf(" Свободной памяти %d МБ — xray может не поместиться.", r.MemAvailKB/1024)
	}
	consent := fmt.Sprintf(" Скачает xray %s с GitHub XTLS на этот компьютер, сверит хеш и положит на роутер (%s, ≈35 МБ в /opt, 30–60 МБ памяти при работе); создаст в Keenetic интерфейс %s и сохранит конфигурацию роутера.",
		xrayVersion, a.bin, firstNonEmpty(tun, "OpkgTun"))
	switch {
	case !ok:
		it.Status, it.Detail = StInfo, "нет сборки xray для "+firstNonEmpty(r.Arch, r.ArchRaw)
	case tun == "":
		it.Status, it.Detail = StInfo, "все интерфейсы OpkgTun1–9 заняты — освободите один в Keenetic"
	case r.XrayReady && r.XrayVersion == xrayVersion:
		it.Status, it.Selectable = StOK, true
		it.Detail = fmt.Sprintf("установлен xray %s · интерфейс %s — отметьте, чтобы переустановить", r.XrayVersion, tun)
	case r.XrayReady:
		it.Status, it.Selectable = StUpgrade, true
		it.Detail = fmt.Sprintf("xray %s → %s (сервер и интерфейс %s сохранятся)", firstNonEmpty(r.XrayVersion, "?"), xrayVersion, tun)
	case r.OptFreeKB > 0 && r.OptFreeKB < xrayNeedKB:
		it.Status = StInfo
		it.Detail = fmt.Sprintf("мало места: свободно %d МБ, xray нужно около %d МБ", r.OptFreeKB/1024, xrayNeedKB/1024)
	default:
		it.Status, it.Selectable = StInstall, true
		it.Detail = "не установлен. Отметьте, чтобы включить список «VLESS»." + consent + mem
	}
	return it
}

// fetchXray downloads the release archive for arch, checks its SHA-256
// against the pinned one and returns the binary inside.
func fetchXray(ctx context.Context, arch string, emit func(Event)) ([]byte, error) {
	a, ok := xrayAssets[arch]
	if !ok {
		return nil, fmt.Errorf("нет сборки xray для %s", arch)
	}
	url := fmt.Sprintf("%s/v%s/%s", xrayBaseURL, xrayVersion, a.zip)
	emit(Event{Kind: "out", Text: "скачиваю " + url})
	ctx, cancel := context.WithTimeout(ctx, xrayFetch)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xray не скачался: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("xray не скачался: HTTP %d", resp.StatusCode)
	}
	z, err := io.ReadAll(io.LimitReader(resp.Body, xrayMaxZip+1))
	if err != nil {
		return nil, fmt.Errorf("xray не скачался: %w", err)
	}
	if len(z) > xrayMaxZip {
		return nil, fmt.Errorf("архив xray больше %d МБ — не тот файл", xrayMaxZip>>20)
	}
	sum := sha256.Sum256(z)
	if got := hex.EncodeToString(sum[:]); got != a.sha256 {
		return nil, fmt.Errorf("хеш архива xray не совпал: %s, ждали %s — файл не тот, что выпустили XTLS; ставить не буду", got, a.sha256)
	}
	emit(Event{Kind: "out", Text: fmt.Sprintf("%s: %d МБ, SHA-256 совпал", a.zip, len(z)>>20)})
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		return nil, fmt.Errorf("архив xray не открылся: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != a.bin {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, xrayMaxBin+1))
		if err != nil {
			return nil, err
		}
		if len(b) > xrayMaxBin {
			return nil, fmt.Errorf("%s в архиве больше %d МБ", a.bin, xrayMaxBin>>20)
		}
		return b, nil
	}
	return nil, fmt.Errorf("в архиве xray нет %s", a.bin)
}

// stepXray puts xray and its init script on the router, makes sure xray
// runs there, creates the OpkgTun interface (description nuxk-vless) and
// saves the router's config, then wires the engine into nuxk.conf. The
// server itself is set later, in the panel.
func stepXray(c *Conn, p *Payload, r *Report, emit func(Event)) error {
	tun := xrayIface(*r)
	if tun == "" {
		return fmt.Errorf("все интерфейсы OpkgTun1–9 заняты")
	}
	bin, err := fetchXray(context.Background(), r.Arch, emit)
	if err != nil {
		return err
	}
	shim, err := p.Read("S52xray-nuxk")
	if err != nil {
		return err
	}
	// a running xray holds its binary on some filesystems
	_ = run(c, emit, fmt.Sprintf("[ -x %[1]s ] && %[1]s stop; true", c.P(pXrayInit)))
	emit(Event{Kind: "out", Text: fmt.Sprintf("xray %s → %s (%d МБ)", xrayVersion, pXray, len(bin)>>20)})
	if err := c.Upload(pXray, bin, "0755"); err != nil {
		return err
	}
	emit(Event{Kind: "out", Text: "S52xray-nuxk → " + pXrayInit})
	if err := c.Upload(pXrayInit, shim, "0755"); err != nil {
		return err
	}
	out, err := c.Output(c.P(pXray)+" version", nil)
	if err != nil || !strings.HasPrefix(out, "Xray ") {
		return fmt.Errorf("xray не запускается на этом роутере (%s): %v", r.ArchRaw, strings.TrimSpace(out))
	}
	emit(Event{Kind: "out", Text: strings.SplitN(out, "\n", 2)[0]})

	// the interface is created once and saved; S52xray-nuxk gives it its
	// address at every start, like usque does for OpkgTun0
	emit(Event{Kind: "out", Text: fmt.Sprintf("Keenetic: интерфейс %s (описание %s), сохранение конфигурации", tun, xrayMark)})
	if err := run(c, emit, fmt.Sprintf(
		`ndmc -c 'show interface %[1]s' >/dev/null 2>&1 || ndmc -c 'interface %[1]s' || exit 1; ndmc -c 'interface %[1]s description %[2]s' && ndmc -c 'system configuration save'`,
		tun, xrayMark)); err != nil {
		return fmt.Errorf("интерфейс %s в Keenetic не создался: %w", tun, err)
	}
	r.XrayReady, r.XrayVersion, r.XrayIface = true, xrayVersion, tun
	emit(Event{Kind: "info", Text: "Сервер задаётся в панели: xray (VLESS) → «Сервер» — ссылка vless:// или подписка 3x-ui. До этого xray не запускается."})
	// an existing nuxk.conf keeps the user's edits: only wire the engine in
	return run(c, emit, fmt.Sprintf(
		`f=%[1]s; [ -f "$f" ] || exit 0
grep -q '^ENGINE_XRAY=' "$f" && sed -i 's|^ENGINE_XRAY=.*|ENGINE_XRAY="%[2]s"|' "$f" || echo 'ENGINE_XRAY="%[2]s"' >>"$f"
grep -q '^PLANE_IFACE_VLESS=' "$f" && sed -i 's|^PLANE_IFACE_VLESS=.*|PLANE_IFACE_VLESS="%[3]s"|' "$f" || echo 'PLANE_IFACE_VLESS="%[3]s"' >>"$f"`,
		c.P(pConf), pXrayInit, tun))
}

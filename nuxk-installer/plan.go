package main

import (
	"fmt"
	"sort"
	"strings"
)

// Status of one plan item, as the form shows it.
type Status string

const (
	StOK      Status = "ok"      // already in place
	StInstall Status = "install" // missing, will be installed
	StUpgrade Status = "upgrade" // present, older than the payload
	StWarn    Status = "warn"    // needs the user (Keenetic web UI), doesn't block the install
	StBlocked Status = "blocked" // install impossible until fixed
	StInfo    Status = "info"    // for the record
)

// Item is one line of the checklist. Selectable items carry an install step.
type Item struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     Status `json:"status"`
	Detail     string `json:"detail"`
	Selectable bool   `json:"selectable"`
	Selected   bool   `json:"selected"`
}

// Plan is the checklist plus the facts the install steps need.
type Plan struct {
	Items   []Item `json:"items"`
	Blocked bool   `json:"blocked"`
	Version string `json:"version"` // payload version to be installed
}

// Step IDs, in execution order.
// usque goes after nfqws2 (its hosts are desynced before it registers) and
// before config (the config names its tunnel); xray likewise.
var stepOrder = []string{"deps", "nfqws2", "core", "usque", "xray", "config", "start"}

var depPkgs = []string{"curl", "ca-certificates", "ipset"}

const minOptFreeKB = 20 * 1024 // core (~7 MB) + web + headroom for opkg

// BuildPlan decides what to do from a Report and what the payload carries.
func BuildPlan(r Report, p *Payload) Plan {
	pl := Plan{Version: p.Version()}
	add := func(it Item) {
		if it.Status == StBlocked {
			pl.Blocked = true
		}
		pl.Items = append(pl.Items, it)
	}

	// --- preconditions ---
	if !r.Entware {
		add(Item{ID: "entware", Title: "Entware", Status: StBlocked,
			Detail: "opkg не найден. Установите Entware на USB-накопитель или во внутреннюю память (компонент «Поддержка открытых пакетов OPKG»), затем подключайтесь по SSH к Entware, а не к CLI роутера."})
	} else {
		d := "opkg на месте"
		if r.KeeneticOS != "" {
			d = fmt.Sprintf("KeeneticOS %s%s", r.KeeneticOS, strOr(" · "+r.Model, r.Model != ""))
		}
		add(Item{ID: "entware", Title: "Entware", Status: StOK, Detail: d})
	}

	switch {
	case r.Arch == "":
		add(Item{ID: "arch", Title: "Архитектура", Status: StBlocked,
			Detail: fmt.Sprintf("%q не поддерживается: nuxk-core собирается под mips, mipsel, aarch64 и x86_64", r.ArchRaw)})
	case !p.HasCore(r.Arch):
		add(Item{ID: "arch", Title: "Архитектура", Status: StBlocked,
			Detail: fmt.Sprintf("%s — в этой сборке инсталлятора нет nuxk-core-%s (соберите через make installer)", r.ArchRaw, r.Arch)})
	default:
		add(Item{ID: "arch", Title: "Архитектура", Status: StOK, Detail: fmt.Sprintf("%s → nuxk-core-%s", r.ArchRaw, r.Arch)})
	}

	if r.Entware && r.OptFreeKB > 0 && r.OptFreeKB < minOptFreeKB {
		add(Item{ID: "space", Title: "Место в /opt", Status: StBlocked,
			Detail: fmt.Sprintf("свободно %d МБ, нужно не меньше %d МБ", r.OptFreeKB/1024, minOptFreeKB/1024)})
	} else if r.OptFreeKB > 0 {
		add(Item{ID: "space", Title: "Место в /opt", Status: StOK, Detail: fmt.Sprintf("свободно %d МБ", r.OptFreeKB/1024)})
	}

	if abs(r.ClockSkew) > 300 {
		add(Item{ID: "clock", Title: "Часы роутера", Status: StWarn,
			Detail: fmt.Sprintf("расходятся на %d мин: HTTPS к репозиториям может не проходить. Включите синхронизацию времени (NTP) в настройках Keenetic.", abs(r.ClockSkew)/60)})
	}

	var missingK []string
	for m, s := range r.Kmods {
		if s == "missing" {
			missingK = append(missingK, m)
		}
	}
	sort.Strings(missingK)
	if len(missingK) > 0 {
		add(Item{ID: "kmods", Title: "Модули ядра Netfilter", Status: StWarn,
			Detail: "нет " + strings.Join(missingK, ", ") + ". В веб-интерфейсе Keenetic: Управление → Параметры системы → Изменить набор компонентов → «Модули ядра подсистемы Netfilter». Без них nfqws2 не запустится; остальное установится."})
	} else if len(r.Kmods) > 0 {
		add(Item{ID: "kmods", Title: "Модули ядра Netfilter", Status: StOK, Detail: "NFQUEUE, connbytes, multiport есть"})
	}

	// --- steps ---
	var missingP []string
	for _, p := range depPkgs {
		if r.Pkgs[p] == "" {
			missingP = append(missingP, p)
		}
	}
	if len(missingP) > 0 {
		add(Item{ID: "deps", Title: "Пакеты Entware", Status: StInstall, Detail: "opkg install " + strings.Join(missingP, " "), Selectable: true, Selected: true})
	} else {
		add(Item{ID: "deps", Title: "Пакеты Entware", Status: StOK, Detail: strings.Join(depPkgs, ", ")})
	}

	if v := r.Pkgs["nfqws2-keenetic"]; v == "" {
		add(Item{ID: "nfqws2", Title: "nfqws2-keenetic", Status: StInstall,
			Detail: "репозиторий nfqws.github.io/nfqws2-keenetic + opkg install nfqws2-keenetic", Selectable: true, Selected: true})
	} else {
		add(Item{ID: "nfqws2", Title: "nfqws2-keenetic", Status: StOK, Detail: "версия " + v + " · обновляется через opkg upgrade"})
	}

	core := Item{ID: "core", Title: "nuxk-core и веб", Selectable: true}
	switch {
	case r.NuxkCore == "":
		core.Status, core.Selected = StInstall, true
		core.Detail = "nuxk-core " + pl.Version + ", init-скрипт, адаптер nfqws2, веб-интерфейс"
	case r.NuxkCore != pl.Version:
		core.Status, core.Selected = StUpgrade, true
		core.Detail = fmt.Sprintf("%s → %s (старый бинарник сохранится как nuxk-core.prev)", r.NuxkCore, pl.Version)
	default:
		core.Status = StOK
		core.Detail = "уже " + pl.Version + " — отметьте, чтобы переустановить"
	}
	add(core)

	if r.NuxkConf {
		add(Item{ID: "config", Title: "Конфиг /opt/etc/nuxk/nuxk.conf", Status: StOK, Detail: "уже есть — сохраняется как есть"})
	} else {
		add(Item{ID: "config", Title: "Конфиг /opt/etc/nuxk/nuxk.conf", Status: StInstall,
			Detail: "будет создан: адрес " + listenAddr(r) + ", новый API-токен", Selectable: true, Selected: true})
	}

	// WARP is opt-in: installing it registers a device with Cloudflare (their
	// terms of service) and creates an OpkgTun interface, saved in the
	// router's config. Ticking the box is the consent.
	usque := Item{ID: "usque", Title: "usque (WARP)"}
	ipk := p.UsqueIPK(r.Arch)
	const consent = " Регистрирует устройство в Cloudflare WARP (вы принимаете их условия), создаёт интерфейс OpkgTun и сохраняет конфигурацию роутера."
	switch {
	case r.UsqueReady:
		usque.Status = StOK
		usque.Detail = "установлен, контракт nuxk есть — будет подключён" + strOr(" · интерфейс "+r.UsqueIface, r.UsqueIface != "")
		if ipk != "" && r.Pkgs["usque-keenetic"] != "" {
			usque.Detail += " · отметьте, чтобы переустановить из этой сборки"
			usque.Selectable = true
		}
	case ipk == "":
		usque.Status = StInfo
		usque.Detail = "в этой сборке инсталлятора нет usque-keenetic для " + firstNonEmpty(r.Arch, r.ArchRaw)
	case r.Init["S51usque"]:
		usque.Status, usque.Selectable = StUpgrade, true
		usque.Detail = "установлен usque без контракта nuxk — заменить на форк nuxk (usque.conf сохранится)." + consent
	default:
		usque.Status, usque.Selectable = StInstall, true
		usque.Detail = "не установлен. Отметьте, чтобы включить список «WARP»." + consent
	}
	add(usque)
	add(planXray(r))

	willChange := false
	for _, it := range pl.Items {
		if it.Selected {
			willChange = true
		}
	}
	if willChange || !r.NuxkRunning {
		add(Item{ID: "start", Title: "Запуск и проверка", Status: StInstall,
			Detail: "S99nuxk-core restart, затем /api/v1/healthz", Selectable: true, Selected: true})
	} else {
		add(Item{ID: "start", Title: "Запуск и проверка", Status: StOK, Detail: "nuxk-core работает — отметьте, чтобы перезапустить", Selectable: true})
	}

	if pl.Blocked {
		for i := range pl.Items {
			pl.Items[i].Selectable, pl.Items[i].Selected = false, false
		}
	}
	return pl
}

// listenAddr is where nuxk-core will listen: the LAN bridge address, so the
// API is reachable from the home network but never bound on WAN interfaces.
func listenAddr(r Report) string {
	if r.NuxkListen != "" {
		return r.NuxkListen
	}
	if r.LANIP != "" {
		return r.LANIP + ":4141"
	}
	return "127.0.0.1:4141"
}

func strOr(s string, ok bool) string {
	if ok {
		return s
	}
	return ""
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

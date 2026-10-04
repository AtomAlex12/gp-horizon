package update

import (
	"errors"
	"fmt"
)

// Components: the parts of nuxk the panel adds to the router — the same
// `nuxk warp`, `nuxk vless`, `nuxk dns` as from SSH, run like an update: in
// the background, reporting into a file, since they restart the agent on the
// way. The files come from this very release, checked against its
// SHA256SUMS; what each does to the router is said before it's asked for.

const (
	componentStatus = "component-run"
	componentLog    = "component.log"
)

// Presence: what the agent knows of a component on this box.
type Presence struct {
	Installed bool
	Version   string
	Cannot    string // why it can't be added here ("" = it can)
}

// Component is one of them, as the panel shows it.
type Component struct {
	ID         string `json:"id"` // nfqws2 | warp | vless | smartdns
	Name       string `json:"name"`
	About      string `json:"about"`   // what it's for
	Confirm    string `json:"confirm"` // what installing does to the router
	Installed  bool   `json:"installed"`
	Version    string `json:"version,omitempty"`
	CanInstall bool   `json:"can_install"`
	Cannot     string `json:"cannot,omitempty"` // why not, when it can't
}

// Components is GET /api/v1/components.
type Components struct {
	Items []Component `json:"items"`
	Run   *Run        `json:"run"` // the last install from the panel
}

var (
	ErrUnknownComponent = errors.New("такого компонента нет")
	ErrInstalled        = errors.New("уже установлен")
	ErrNoInstall        = errors.New("поставить отсюда нельзя")
)

type piece struct{ id, cmd, name, about, confirm string }

const restarts = " Агент nuxk перезапустится — панель пропадёт на 10–30 секунд."

var pieces = []piece{
	{"nfqws2", "nfqws2", "nfqws2 (обход DPI)",
		"Штатный пакет nfqws2-keenetic: обходит блокировки по DPI без туннеля; nuxk ведёт его списки.",
		"Подключит репозиторий nfqws2-keenetic и поставит пакет через opkg. Штатный nfqws2 сразу начнёт обрабатывать трафик по своему конфигу." + restarts},
	{"warp", "warp", "WARP (usque)",
		"Туннель Cloudflare WARP: без своего сервера, для сайтов, закрытых по адресу.",
		"Зарегистрирует роутер в Cloudflare WARP (вы принимаете их условия), создаст интерфейс OpkgTun и сохранит конфигурацию роутера." + restarts},
	{"vless", "vless", "VLESS (xray)",
		"Туннель через ваш сервер VLESS (например, 3x-ui). Сервер задаётся потом на странице «xray (VLESS)».",
		"Скачает xray с GitHub XTLS со сверкой хеша (около 35 МБ в /opt, 30–60 МБ памяти), создаст в Keenetic интерфейс OpkgTun и сохранит конфигурацию роутера." + restarts},
	{"smartdns", "dns", "SmartDNS (бета)",
		"Отвечает на DNS вместо встроенного DNS nuxk: свой кэш, заранее обновляемые имена. Включается в «DNS» → «Настройки».",
		"Скачает SmartDNS с GitHub автора со сверкой хеша в /opt. Сам не запустится, пока его не выбрать в «DNS» → «Настройки»." + restarts},
}

// Components: each part, whether it's here, whether the panel can add it.
func (u *Updater) Components() Components {
	c := Components{Items: []Component{}, Run: u.readRunAt(componentStatus, componentLog)}
	why := u.cannot()
	if u.o.Have == nil {
		why = "компоненты ставятся из панели только на роутере"
	}
	busy := u.busy()
	for _, p := range pieces {
		it := Component{ID: p.id, Name: p.name, About: p.about, Confirm: p.confirm}
		if u.o.Have != nil {
			pr := u.o.Have(p.id)
			it.Installed, it.Version, it.Cannot = pr.Installed, pr.Version, pr.Cannot
		}
		switch {
		case it.Installed:
			it.Cannot = ""
		case why != "":
			it.Cannot = why
		case it.Cannot == "" && busy:
			it.Cannot = "идёт обновление или установка — дождитесь конца"
		}
		it.CanInstall = !it.Installed && it.Cannot == ""
		c.Items = append(c.Items, it)
	}
	return c
}

// Install starts `nuxk <part> --yes` in the background and returns at once;
// the panel follows the run in Components().Run.
func (u *Updater) Install(id string) (Components, error) {
	u.mu.Lock()
	err := u.installLocked(id)
	u.mu.Unlock()
	return u.Components(), err
}

func (u *Updater) installLocked(id string) error {
	var p *piece
	for i := range pieces {
		if pieces[i].id == id {
			p = &pieces[i]
		}
	}
	if p == nil {
		return ErrUnknownComponent
	}
	if u.o.Have == nil {
		return fmt.Errorf("%w: компоненты ставятся из панели только на роутере", ErrNoInstall)
	}
	pr := u.o.Have(id)
	if pr.Installed {
		return ErrInstalled
	}
	if why := u.cannot(); why != "" {
		return fmt.Errorf("%w: %s", ErrNoInstall, why)
	}
	if pr.Cannot != "" {
		return fmt.Errorf("%w: %s", ErrNoInstall, pr.Cannot)
	}
	if u.busy() {
		return ErrBusy
	}
	return u.spawn(script{
		args: []string{p.cmd, "--yes"}, status: componentStatus, log: componentLog,
		first: fmt.Sprintf("task %s\nmessage Запускаю установку: %s\n", id, p.name),
		env:   []string{"NUXK_TASK=" + id},
	})
}

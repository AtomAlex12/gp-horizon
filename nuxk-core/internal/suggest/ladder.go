package suggest

import (
	"context"
	"fmt"
	"net"
	"strings"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
)

// The ladder. Two of them, by what is wrong:
//
//   - the site itself refuses Russia (geo): only VLESS helps — WARP leaves
//     the network in Moscow (Cloudflare DME), so the site still sees Russia;
//   - the provider cuts or slows the site (SNI, IP, the ~16 KB freeze, too
//     slow): DPI (nfqws2) if it is wired, then WARP, then VLESS — VLESS only
//     when WARP doesn't do. DPI can't be tried before the domain is in its
//     list, so it is suggested unconfirmed and checked after acceptance.
//
// Ways are measured one at a time, cheapest first, and the walk stops as soon
// as the recommendation is known: the first way that is good enough wins, not
// the fastest one. Nothing here changes routing.

// mode → the way a probe takes to try it.
var modeVia = map[string]probe.Via{
	string(plane.ModeDesync): probe.ViaDesync, string(plane.ModeWarp): probe.ViaWarp, string(plane.ModeVless): probe.ViaVless,
}

var title = map[probe.Via]string{
	probe.ViaDirect: "напрямую", probe.ViaDesync: "DPI (nfqws2)", probe.ViaWarp: "WARP", probe.ViaVless: "VLESS",
}

const (
	noWarpForGeo = " WARP тут не поможет: он выходит в интернет в Москве (Cloudflare DME), и сайт видит Россию."
	dpiUnchecked = " Обычно помогает «DPI» (nfqws2). Проверить это можно только после того, как вы примете предложение: до этого домена нет в списке nfqws2."
)

type ladder struct {
	p      Prober
	bar    bar
	desync bool // nfqws2 is wired
	domain string
	ip     net.IP
	it     *Item
	seen   map[probe.Via]judged
}

type judged struct {
	m   probe.Result
	ok  bool
	why string // why not good enough
}

// decide runs the ladder for one domain. State, source and times are the
// caller's to fill.
func decide(ctx context.Context, p Prober, b bar, desync bool, domain string) Item {
	it := Item{Domain: domain}
	ip, why := p.Resolve(ctx, domain)
	switch why {
	case "":
	case "bogus":
		it.Verdict = string(probe.VerdictDNS)
		it.Reason = "DNS вернул заглушку (частный или нулевой адрес) — ответ подменён. Маршрут тут не поможет: включите защищённый DNS (раздел «DNS») — он спрашивает в обход провайдера."
		it.Paths = []Path{{Via: "dns", Result: "подменённый ответ"}}
		return it
	case "nxdomain":
		it.Verdict, it.Reason = string(probe.VerdictUnknown), "Такого домена нет, либо DNS его скрывает."
		it.Paths = []Path{{Via: "dns", Result: "имя не найдено"}}
		return it
	default:
		it.Verdict, it.Reason = string(probe.VerdictUnknown), "DNS не ответил вовремя."
		it.Paths = []Path{{Via: "dns", Result: "нет ответа"}}
		return it
	}
	l := &ladder{p: p, bar: b, desync: desync, domain: domain, ip: ip, it: &it, seen: map[probe.Via]judged{}}
	if l.measure(ctx, probe.ViaDirect).ok {
		it.Verdict, it.Reason = string(probe.VerdictOK), "Открывается напрямую — ничего делать не нужно."
		return it
	}
	l.afterDirect(ctx)
	return it
}

// measure walks one way, judges it and adds it to the item's paths.
func (l *ladder) measure(ctx context.Context, via probe.Via) judged {
	if j, ok := l.seen[via]; ok {
		return j
	}
	m := l.p.Way(ctx, l.domain, l.ip, via)
	m.Via = via
	ok, why := l.bar.judge(m)
	j := judged{m, ok, why}
	l.seen[via] = j
	l.it.Paths = append(l.it.Paths, describe(j))
	return j
}

// afterDirect reads why direct isn't good enough and takes the matching ladder.
func (l *ladder) afterDirect(ctx context.Context) {
	j := l.seen[probe.ViaDirect]
	d := j.m
	switch {
	case d.Clean(): // opens, just not well enough
		l.provider(ctx, probe.VerdictSlow, true, "Напрямую открывается, но "+j.why+".")
	case d.TLS && d.Status > 0 && d.Signal == probe.SignalExplicit:
		l.geo(ctx, true, fmt.Sprintf("Сайт сам отказывает по стране (HTTP %d).", d.Status))
	case d.TLS && d.Status > 0 && (d.Signal == probe.SignalChallenge || d.Signal == probe.SignalDenied):
		what := "отказ (HTTP 403)"
		if d.Signal == probe.SignalChallenge {
			what = "проверку «Just a moment»"
		}
		l.geo(ctx, false, "Напрямую сайт даёт "+what+".")
	case d.Frozen:
		l.provider(ctx, probe.VerdictFreeze, true, "Соединение устанавливается, но замирает после первых ~16 КБ — так ТСПУ режет зарубежные хостинги и CDN.")
	case d.Opened > 0:
		l.provider(ctx, probe.VerdictSlow, true, "Напрямую открывается не каждый раз ("+j.why+").")
	case d.Cut && d.Status < 500: // a broken site's own error page cut short is the site's fault
		l.provider(ctx, probe.VerdictFreeze, true, fmt.Sprintf("Ответ начинает приходить, но соединение обрывается посреди страницы (после %d КБ) — так ТСПУ режет зарубежные хостинги и CDN.", d.Bytes>>10))
	case d.TLS && d.Status > 0:
		l.it.Verdict = string(probe.VerdictUnknown)
		l.it.Reason = fmt.Sprintf("Сайт отвечает, но не открывается нормально (HTTP %d) — это его неполадка, маршрут тут не поможет.", d.Status)
	case d.TLS && d.HTTPFail == probe.FailTimeout:
		l.provider(ctx, probe.VerdictFreeze, true, "Рукопожатие проходит, а ответа на запрос нет — похоже на заморозку соединения ТСПУ.")
	case d.TLS:
		l.provider(ctx, probe.VerdictUnknown, false, "Рукопожатие проходит, но соединение обрывается на запросе ("+failText[d.HTTPFail]+").")
	case d.TCP && d.TLSFail == probe.FailAlert:
		l.it.Verdict = string(probe.VerdictUnknown)
		l.it.Reason = "Сервер сам отверг TLS-рукопожатие — это не похоже на блокировку провайдером."
	case d.TCP && d.Neutral != nil && !*d.Neutral:
		l.provider(ctx, probe.VerdictIP, true, "Адрес принимает подключение, но рукопожатие TLS не проходит ни с каким именем — похоже на блокировку по IP.")
	case d.TCP:
		l.provider(ctx, probe.VerdictSNI, true, "Соединение обрывается на имени сайта (SNI), а на нейтральное имя тот же адрес отвечает — это DPI провайдера.")
	default:
		why := "Адрес сайта напрямую не отвечает (" + failText[d.TCPFail] + ") — похоже на блокировку по IP."
		if l.desync {
			// nothing for nfqws2 to work on: no connection, no handshake to bend
			why += " DPI тут не поможет: соединение даже не устанавливается."
		}
		l.provider(ctx, probe.VerdictIP, false, why)
	}
}

// geo: the site refuses this country. Only VLESS is tried.
func (l *ladder) geo(ctx context.Context, explicit bool, why string) {
	it := l.it
	it.Verdict = string(probe.VerdictGeo)
	v := l.measure(ctx, probe.ViaVless)
	switch {
	case v.ok:
		it.Mode, it.Confirmed = string(plane.ModeVless), true
		it.Reason = why + " Через VLESS открывается (проверено)." + noWarpForGeo
	case v.m.Skipped != "" && explicit:
		it.Mode = string(plane.ModeVless)
		it.Reason = why + " Нужен VLESS — выход за границей; проверить его сейчас нельзя (" + v.m.Skipped + "), поэтому вывод непроверенный." + noWarpForGeo
	case v.m.Skipped != "":
		it.Verdict = string(probe.VerdictUnknown)
		it.Reason = why + " Отказ по стране это ещё не доказывает, а сравнить через VLESS нечем (" + v.m.Skipped + ")."
	case v.m.Clean(): // opens there, slowly — still the only way
		it.Mode = string(plane.ModeVless)
		it.Reason = why + " Через VLESS открывается, но " + v.why + "." + noWarpForGeo
	case explicit:
		it.Reason = why + " Через VLESS тоже не открылся (" + describe(v).Result + ") — возможно, туннель не работает или его выход тоже в неподходящей стране."
	default:
		it.Verdict = string(probe.VerdictUnknown)
		it.Reason = why + " Через VLESS тоже не открылся (" + describe(v).Result + "), так что отказ по стране это не доказывает."
	}
}

// provider: the provider cuts or slows the site. dpi: nfqws2 may help
// (there is a connection for it to work on).
func (l *ladder) provider(ctx context.Context, v probe.Verdict, dpi bool, why string) {
	it := l.it
	it.Verdict = string(v)
	ok, slow := l.tunnels(ctx)
	// "Slow" alone doesn't say the provider is to blame: a way out that does
	// better has to show it before anything is suggested.
	const notBetter = " Через туннели тоже не дотягивает до порогов — похоже, дело в самом сайте или в канале, а не в провайдере."
	if dpi && l.desync {
		switch {
		case ok != "":
			it.Mode, it.Alt = string(plane.ModeDesync), string(ok)
			it.Reason = why + dpiUnchecked + " Запасной путь — " + title[ok] + ": через него открывается (проверено)."
		case v == probe.VerdictSlow && !l.tried():
			it.Reason = why + " Сравнить через туннели нечем (" + l.noWay() + "), а без этого не видно, что виноват провайдер."
		case v == probe.VerdictSlow:
			it.Reason = why + notBetter
		default:
			it.Mode = string(plane.ModeDesync)
			it.Reason = why + dpiUnchecked
			if l.tried() {
				it.Reason += " Туннели не выручают: " + l.summary(probe.ViaWarp, probe.ViaVless) + "."
			}
		}
		return
	}
	switch {
	case ok != "":
		it.Mode, it.Confirmed = string(ok), true
		it.Reason = why + " Через " + title[ok] + " открывается (проверено)."
	case !l.tried():
		it.Reason = why + " Обойти нечем: " + l.noWay() + "."
	case v == probe.VerdictSlow:
		it.Reason = why + notBetter
	case slow != "":
		it.Mode = string(slow)
		it.Reason = why + " Через " + title[slow] + " открывается, но " + l.seen[slow].why + "."
	case v == probe.VerdictIP && !dpi:
		it.Verdict = string(probe.VerdictUnknown)
		it.Reason = "Не открывается ни напрямую, ни через туннели (" + l.summary(probe.ViaWarp, probe.ViaVless) + ") — сайт, похоже, не работает или нужен другой выход."
	default:
		it.Reason = why + " Через туннели тоже не открывается: " + l.summary(probe.ViaWarp, probe.ViaVless) + "."
	}
}

// tunnels measures WARP, then VLESS only if WARP doesn't do. ok: the first
// good enough; slow: one that opens the site, but below the thresholds.
func (l *ladder) tunnels(ctx context.Context) (ok, slow probe.Via) {
	for _, via := range []probe.Via{probe.ViaWarp, probe.ViaVless} {
		j := l.measure(ctx, via)
		if j.ok {
			return via, ""
		}
		if slow == "" && j.m.Clean() {
			slow = via
		}
	}
	return "", slow
}

// tried: some tunnel was really walked, not skipped.
func (l *ladder) tried() bool {
	for via, j := range l.seen {
		if via != probe.ViaDirect && j.m.Skipped == "" {
			return true
		}
	}
	return false
}

func (l *ladder) noWay() string {
	var parts []string
	if !l.desync {
		parts = append(parts, "nfqws2 не подключён")
	}
	for _, via := range []probe.Via{probe.ViaWarp, probe.ViaVless} {
		if j, ok := l.seen[via]; ok && j.m.Skipped != "" {
			parts = append(parts, title[via]+" — "+j.m.Skipped)
		}
	}
	return strings.Join(parts, "; ")
}

// summary: what each measured way did, in a few words.
func (l *ladder) summary(vias ...probe.Via) string {
	var parts []string
	for _, via := range vias {
		if j, ok := l.seen[via]; ok {
			parts = append(parts, title[via]+": "+describe(j).Result)
		}
	}
	return strings.Join(parts, "; ")
}

var failText = map[probe.Fail]string{
	probe.FailTimeout: "тишина (таймаут)", probe.FailReset: "обрыв соединения (RST)", probe.FailEOF: "соединение закрыто",
	probe.FailRefused: "отказ в соединении", probe.FailUnreachable: "нет маршрута", probe.FailAlert: "сервер отверг TLS", probe.FailOther: "ошибка",
	probe.FailNone: "ошибка",
}

// describe renders a measured way for the details row.
func describe(j judged) Path {
	m := j.m
	p := Path{Via: string(m.Via), Ms: m.Ms, Tries: m.Tries, Opened: m.Opened, TTFBMs: m.TTFBMs, Kbps: m.Kbps, Frozen: m.Frozen, Acceptable: j.ok}
	switch {
	case m.Skipped != "":
		p.Result, p.Ms = "не проверялся: "+m.Skipped, 0
	case j.ok:
		p.Result = "открывается"
		// the status is the first try's: a refusal forgiven by the 4-in-5 rule isn't "opens (HTTP 403)"
		if m.Status > 0 && m.Status < 400 && m.Signal == probe.SignalNone {
			p.Result += fmt.Sprintf(" (HTTP %d)", m.Status)
		}
	case m.Clean():
		p.Result = "открывается, но " + j.why
	case m.Frozen:
		p.Result = "замирает после первых ~16 КБ"
	case m.Opened > 0:
		p.Result = j.why
	case m.Cut:
		p.Result = fmt.Sprintf("обрывается посреди страницы (после %d КБ)", m.Bytes>>10)
	case !m.TCP:
		p.Result = "не подключается: " + failText[m.TCPFail]
	case !m.TLS:
		p.Result = "TLS: " + failText[m.TLSFail]
		if m.Neutral != nil {
			if *m.Neutral {
				p.Result += "; на другое имя тот же адрес отвечает"
			} else {
				p.Result += "; на другое имя тоже молчит"
			}
		}
	case m.Status == 0:
		p.Result = "HTTP: " + failText[m.HTTPFail]
	case m.Signal == probe.SignalExplicit:
		p.Result = fmt.Sprintf("отказ по стране (HTTP %d)", m.Status)
	case m.Signal == probe.SignalChallenge:
		p.Result = fmt.Sprintf("проверка «Just a moment» (HTTP %d)", m.Status)
	case m.Signal == probe.SignalDenied:
		p.Result = "отказ (HTTP 403)"
	case m.Status >= 500:
		p.Result = fmt.Sprintf("ошибка сервера (HTTP %d)", m.Status)
	default:
		p.Result = fmt.Sprintf("не открывается нормально (HTTP %d)", m.Status)
	}
	return p
}

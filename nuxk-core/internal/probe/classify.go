package probe

// Verdict is what is wrong with reaching a domain directly.
type Verdict string

const (
	VerdictOK      Verdict = "ok"        // opens normally, nothing to do
	VerdictGeo     Verdict = "geoblock"  // the site itself refuses this country
	VerdictSNI     Verdict = "sni-block" // the provider cuts the connection by the site's name
	VerdictIP      Verdict = "ip-block"  // the provider cuts the address itself
	VerdictDNS     Verdict = "dns-block" // DNS gives a stub instead of the site
	VerdictUnknown Verdict = "unknown"   // no safe conclusion
)

// Suggestion is the verdict plus the way out it points to.
type Suggestion struct {
	Verdict Verdict
	// Mode is the plane list to put the domain in: desync | warp | vless, or ""
	// when routing would not help (or nothing can be said).
	Mode string
	// Alt is a second way out that was actually tried and works ("" = none).
	Alt string
	// Confirmed: the site really opened through Mode. False means "this is what
	// the evidence points to" — e.g. DPI desync can't be tried without
	// changing the router.
	Confirmed bool
	Reason    string // for the user, in Russian
}

// Options says what the box can do beyond tunnels.
type Options struct {
	Desync bool // nfqws2 is wired, so a DPI list is a real option
}

var viaTitle = map[Via]string{ViaVless: "VLESS", ViaWarp: "WARP"}

// Classify reads a Report. The rule that keeps it honest: a tunnel is
// recommended as a fix only for something we can tell apart from a site that
// is simply down — a bare 403 or a challenge page counts as geo-blocking only
// when a tunnel opens the same site.
func Classify(r Report, o Options) Suggestion {
	switch r.DNS {
	case "":
	case "bogus":
		return Suggestion{Verdict: VerdictDNS, Reason: "DNS вернул заглушку (частный или нулевой адрес) — похоже на подмену ответа. Маршрут тут не поможет: нужен честный DNS."}
	case "nxdomain":
		return Suggestion{Verdict: VerdictUnknown, Reason: "Такого домена нет, либо DNS его скрывает."}
	default:
		return Suggestion{Verdict: VerdictUnknown, Reason: "DNS не ответил вовремя."}
	}

	d := r.Direct
	tun, tunOK := workingTunnel(r)
	tried := triedTunnels(r)

	switch {
	case d.Clean():
		return Suggestion{Verdict: VerdictOK, Reason: "Открывается напрямую."}

	case d.TCP && d.TLS: // reached the server and was turned away
		switch d.Signal {
		case SignalExplicit:
			switch {
			case tunOK:
				return Suggestion{Verdict: VerdictGeo, Mode: string(tun), Confirmed: true,
					Reason: "Сайт сам отказывает по стране, а через " + viaTitle[tun] + " открывается."}
			case tried:
				return Suggestion{Verdict: VerdictGeo,
					Reason: "Сайт отказывает по стране, но и через туннель не открылся — возможно, выход туннеля тоже блокируется."}
			default:
				return Suggestion{Verdict: VerdictGeo, Mode: string(ViaVless),
					Reason: "Сайт сам отказывает по стране. Проверить через туннель не удалось (" + whyNoTunnel(r) + "), поэтому вывод непроверенный."}
			}
		case SignalChallenge, SignalDenied:
			if tunOK {
				what := "отказ (403)"
				if d.Signal == SignalChallenge {
					what = "проверку «Just a moment»"
				}
				return Suggestion{Verdict: VerdictGeo, Mode: string(tun), Confirmed: true,
					Reason: "Напрямую сайт даёт " + what + ", а через " + viaTitle[tun] + " открывается нормально."}
			}
			return Suggestion{Verdict: VerdictUnknown,
				Reason: "Сайт отвечает отказом или проверкой, но причину по стране это не доказывает: через туннель подтвердить не удалось (" + whyNoTunnel(r) + ")."}
		}
		return Suggestion{Verdict: VerdictUnknown, Reason: "Сайт отвечает, но не открывается нормально (ошибка сервера или оборванный ответ)."}

	case d.TCP && !d.TLS: // connected, then the handshake died
		if d.TLSFail == FailAlert {
			return Suggestion{Verdict: VerdictUnknown, Reason: "Сервер сам отверг TLS-рукопожатие — это не похоже на блокировку провайдером."}
		}
		if d.Neutral != nil && !*d.Neutral { // even an unrelated name gets nothing from this IP
			return ipBlock(r, tun, tunOK, tried)
		}
		s := Suggestion{Verdict: VerdictSNI}
		switch {
		case o.Desync:
			s.Mode = "desync"
			s.Reason = "Соединение обрывается на имени сайта (SNI), а на нейтральное имя тот же адрес отвечает — это DPI. Обычно помогает «DPI» (nfqws2); проверить без изменения роутера нельзя."
			if tunOK {
				s.Alt = string(tun)
				s.Reason += " Через " + viaTitle[tun] + " сайт открывается."
			}
		case tunOK:
			s.Mode, s.Confirmed = string(tun), true
			s.Reason = "Соединение обрывается на имени сайта (SNI) — это DPI. Через " + viaTitle[tun] + " открывается."
		default:
			s.Reason = "Соединение обрывается на имени сайта (SNI), но способа обхода под рукой нет: nfqws2 не подключён, туннели не проверены (" + whyNoTunnel(r) + ")."
		}
		return s

	default: // no TCP at all
		return ipBlock(r, tun, tunOK, tried)
	}
}

func ipBlock(r Report, tun Via, tunOK, tried bool) Suggestion {
	switch {
	case tunOK:
		return Suggestion{Verdict: VerdictIP, Mode: string(tun), Confirmed: true,
			Reason: "Адрес сайта напрямую не отвечает, а через " + viaTitle[tun] + " открывается — похоже на блокировку по IP."}
	case tried:
		return Suggestion{Verdict: VerdictUnknown, Reason: "Не открывается ни напрямую, ни через туннель — сайт лежит или нужен другой выход."}
	}
	return Suggestion{Verdict: VerdictUnknown, Reason: "Напрямую не открывается, а проверить туннелем нечем (" + whyNoTunnel(r) + ")."}
}

// workingTunnel is the first tunnel that opened the site.
func workingTunnel(r Report) (Via, bool) {
	for _, t := range r.Tunnels {
		if t.Clean() {
			return t.Via, true
		}
	}
	return "", false
}

// triedTunnels: at least one tunnel was really probed (not skipped).
func triedTunnels(r Report) bool {
	for _, t := range r.Tunnels {
		if t.Skipped == "" {
			return true
		}
	}
	return false
}

// whyNoTunnel is the first reason a tunnel was skipped, for the user.
func whyNoTunnel(r Report) string {
	for _, t := range r.Tunnels {
		if t.Skipped != "" {
			return viaTitle[t.Via] + ": " + t.Skipped
		}
	}
	return "туннелей нет"
}

package plane

import (
	"sort"
	"strings"
)

// infraNames: what the router itself must reach straight — time servers, the
// DoH resolvers, WARP's registration. A WARP or VLESS entry covering one of
// them (domain routing covers subdomains) sends the router's own traffic into
// the tunnel: when the tunnel is down — a reboot, a server outage — the clock
// or DNS go down with it, and a wrong clock in turn breaks TLS and Reality.
// On 10.10.2026 «cloudflare.com» in VLESS was found taking time.cloudflare.com
// along: the devices' clocks synced through the tunnel, and a router set to
// that server would lose its clock whenever VLESS is down.
var infraNames = map[string]string{
	"time.cloudflare.com":         "сервер времени",
	"time.google.com":             "сервер времени",
	"pool.ntp.org":                "серверы времени",
	"time.windows.com":            "сервер времени",
	"time.apple.com":              "сервер времени",
	"cloudflare-dns.com":          "DoH Cloudflare",
	"dns.google":                  "DoH Google",
	"dns.quad9.net":               "DoH Quad9",
	"engage.cloudflareclient.com": "регистрация WARP",
	"api.cloudflareclient.com":    "регистрация WARP",
}

// InfraWarnings: one warning per WARP/VLESS entry that covers the router's
// own infrastructure.
func InfraWarnings(d Desired) []string {
	var out []string
	for _, l := range d.Lists {
		if !l.Mode.Routed() {
			continue
		}
		for _, entry := range l.Domains {
			entry = strings.ToLower(strings.TrimSuffix(entry, "."))
			var hit []string
			for name, what := range infraNames {
				if name == entry || strings.HasSuffix(name, "."+entry) {
					hit = append(hit, name+" ("+what+")")
				}
			}
			if len(hit) == 0 {
				continue
			}
			sort.Strings(hit)
			out = append(out, "«"+entry+"» в списке "+modeName(l.Mode)+" уводит в туннель и "+strings.Join(hit, ", ")+
				": когда туннель лежит, роутер не синхронизирует время или DNS, а с неверными часами не работают TLS и Reality. "+
				"Укажите в Keenetic сервер времени, который не попадает в списки, или уберите домен из списка.")
		}
	}
	return out
}

func modeName(m Mode) string {
	if m == ModeWarp {
		return "WARP"
	}
	return "VLESS"
}

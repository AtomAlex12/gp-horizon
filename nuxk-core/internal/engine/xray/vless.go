package xray

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

// Server is one VLESS server, as a 3x-ui vless:// link describes it.
type Server struct {
	Name        string   // the link's #fragment (the panel's remark)
	ID          string   // the user's id — the secret
	Host        string   // address the client dials
	Port        int      //
	Flow        string   // xtls-rprx-vision or ""
	Network     string   // raw (tcp) | ws | grpc | xhttp | httpupgrade
	Security    string   // none | tls | reality
	SNI         string   // TLS / Reality server name
	Fingerprint string   // uTLS fingerprint, chrome by default
	PublicKey   string   // Reality pbk
	ShortID     string   // Reality sid
	SpiderX     string   // Reality spx
	ALPN        []string // TLS
	Path        string   // ws / httpupgrade / xhttp
	HostHeader  string   // ws / httpupgrade / xhttp "host"
	ServiceName string   // grpc
	Mode        string   // xhttp
	Encryption  string   // VLESS encryption, "none" unless the server set one
}

var (
	idRe     = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	tokenRe  = regexp.MustCompile(`^[A-Za-z0-9._~:-]{1,253}$`) // sni, fp, sid, flow, mode, …
	keyRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{32,64}$`)    // Reality public key, base64url
	alpnRe   = regexp.MustCompile(`^[A-Za-z0-9./-]{1,32}$`)    // h2, http/1.1
	encRe    = regexp.MustCompile(`^[A-Za-z0-9._+/=-]+$`)      // and ≤ 2 KiB (post-quantum keys are long)
	flows    = map[string]bool{"": true, "xtls-rprx-vision": true, "xtls-rprx-vision-udp443": true}
	networks = map[string]string{"": "raw", "tcp": "raw", "raw": "raw", "ws": "ws", "grpc": "grpc", "xhttp": "xhttp", "splithttp": "xhttp", "httpupgrade": "httpupgrade"}
)

// ParseVLESS reads a vless:// link. Errors name the field, never the id.
func ParseVLESS(link string) (Server, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "vless" || u.User == nil {
		return Server{}, bad("это не ссылка vless://")
	}
	q := u.Query()
	s := Server{
		Name:        u.Fragment,
		ID:          u.User.Username(),
		Host:        strings.Trim(u.Hostname(), "[]"),
		Flow:        q.Get("flow"),
		Security:    q.Get("security"),
		SNI:         q.Get("sni"),
		Fingerprint: q.Get("fp"),
		PublicKey:   q.Get("pbk"),
		ShortID:     q.Get("sid"),
		SpiderX:     q.Get("spx"),
		Path:        q.Get("path"),
		HostHeader:  q.Get("host"),
		ServiceName: q.Get("serviceName"),
		Mode:        q.Get("mode"),
		Encryption:  q.Get("encryption"),
	}
	nw, ok := networks[q.Get("type")]
	if !ok {
		return Server{}, bad("транспорт type=%q не поддерживается (tcp, ws, grpc, xhttp, httpupgrade)", q.Get("type"))
	}
	s.Network = nw
	if s.Security == "" {
		s.Security = "none"
	}
	if s.Encryption == "" {
		s.Encryption = "none"
	}
	if a := q.Get("alpn"); a != "" {
		s.ALPN = strings.Split(a, ",")
	}
	if h := q.Get("headerType"); h != "" && h != "none" {
		return Server{}, bad("headerType=%q не поддерживается", h)
	}
	if s.Port, err = strconv.Atoi(u.Port()); err != nil || s.Port < 1 || s.Port > 65535 {
		return Server{}, bad("нет порта сервера")
	}
	return s, s.validate()
}

func (s Server) validate() error {
	switch {
	case !idRe.MatchString(s.ID):
		return bad("нет id пользователя (UUID) перед @")
	case s.Host == "" || (net.ParseIP(s.Host) == nil && !engine.ValidDomain(strings.ToLower(s.Host))):
		return bad("адрес сервера — не имя и не IP")
	case s.Security != "none" && s.Security != "tls" && s.Security != "reality":
		return bad("security=%q не поддерживается (none, tls, reality)", s.Security)
	case !flows[s.Flow]:
		return bad("flow=%q не поддерживается", s.Flow)
	case s.Flow != "" && (s.Network != "raw" || s.Security == "none"):
		return bad("flow vision работает только с type=tcp и tls/reality")
	case !encRe.MatchString(s.Encryption) || len(s.Encryption) > 2048:
		return bad("encryption — непонятное значение")
	}
	for name, v := range map[string]string{"sni": s.SNI, "fp": s.Fingerprint, "sid": s.ShortID, "mode": s.Mode, "serviceName": s.ServiceName, "host": s.HostHeader} {
		if v != "" && !tokenRe.MatchString(v) {
			return bad("%s — непонятное значение", name)
		}
	}
	if s.Security == "reality" {
		if !keyRe.MatchString(s.PublicKey) {
			return bad("для Reality нужен публичный ключ pbk")
		}
		if s.SNI == "" {
			return bad("для Reality нужен sni")
		}
	}
	for _, a := range s.ALPN {
		if !alpnRe.MatchString(a) {
			return bad("alpn — непонятное значение")
		}
	}
	return nil
}

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", engine.ErrBadConfig, fmt.Sprintf(format, a...))
}

type obj = map[string]any

// Render is xray's config.json for this server: the VLESS outbound (first,
// so everything goes there) and a direct one. The TUN inbound is not here:
// S52xray-nuxk keeps it in its own tun.json and runs xray with both files,
// because `xray run -test` on a config with a TUN inbound opens the device —
// which the running xray holds, so no new server could ever be tested.
// Which traffic enters the TUN device is the router's business (Keenetic
// routes the VLESS list to OpkgTunN); xray's own connection to the server
// takes the ordinary default route.
func Render(s Server) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	user := obj{"id": s.ID, "encryption": s.Encryption}
	if s.Flow != "" {
		user["flow"] = s.Flow
	}
	stream := obj{"network": s.Network, "security": s.Security}
	fp := s.Fingerprint
	if fp == "" {
		fp = "chrome"
	}
	switch s.Security {
	case "reality":
		stream["realitySettings"] = obj{"serverName": s.SNI, "fingerprint": fp, "publicKey": s.PublicKey, "shortId": s.ShortID, "spiderX": s.SpiderX}
	case "tls":
		tls := obj{"serverName": s.SNI, "fingerprint": fp}
		if s.SNI == "" {
			tls["serverName"] = s.Host
		}
		if len(s.ALPN) > 0 {
			tls["alpn"] = s.ALPN
		}
		stream["tlsSettings"] = tls
	}
	switch s.Network {
	case "ws":
		stream["wsSettings"] = obj{"path": s.Path, "host": s.HostHeader}
	case "httpupgrade":
		stream["httpupgradeSettings"] = obj{"path": s.Path, "host": s.HostHeader}
	case "grpc":
		stream["grpcSettings"] = obj{"serviceName": s.ServiceName}
	case "xhttp":
		mode := s.Mode
		if mode == "" {
			mode = "auto"
		}
		stream["xhttpSettings"] = obj{"path": s.Path, "host": s.HostHeader, "mode": mode}
	}
	cfg := obj{
		"log": obj{"loglevel": "warning"},
		"outbounds": []any{
			obj{
				"tag": "vless", "protocol": "vless",
				"settings":       obj{"vnext": []any{obj{"address": s.Host, "port": s.Port, "users": []any{user}}}},
				"streamSettings": stream,
			},
			obj{"tag": "direct", "protocol": "freedom"},
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// meta is what S52xray-nuxk reports back in info (config.* keys): one
// "key value" line each, no secrets.
func meta(s Server, endpoint, iface string) string {
	var b strings.Builder
	for _, kv := range [][2]string{
		{"server", net.JoinHostPort(s.Host, strconv.Itoa(s.Port))},
		{"endpoint", endpoint},
		{"security", s.Security},
		{"network", s.Network},
		{"sni", s.SNI},
		{"fingerprint", s.Fingerprint},
		{"flow", s.Flow},
		{"iface", iface},
	} {
		if kv[1] != "" {
			b.WriteString(kv[0] + " " + kv[1] + "\n")
		}
	}
	return b.String()
}

// endpointOf: the server as ip:port for nfqws2's endpoints list — resolved
// once, when the config is set; "" when the name doesn't resolve.
func endpointOf(ctx context.Context, s Server) string {
	port := strconv.Itoa(s.Port)
	if ip := net.ParseIP(s.Host); ip != nil {
		return net.JoinHostPort(ip.String(), port)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", s.Host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return net.JoinHostPort(ips[0].String(), port)
}

// Subscription fetches a 3x-ui subscription and returns its VLESS servers,
// in the panel's order. The body is base64 of one link per line (plain text
// is accepted too); links of other protocols are skipped.
func Subscription(ctx context.Context, client *http.Client, link string) ([]Server, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, bad("подписка — это ссылка http(s)://")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, bad("подписка — непонятная ссылка")
	}
	resp, err := client.Do(req)
	if err != nil {
		// not the url.Error itself: the link carries the subscription's secret
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("подписка не скачалась: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("подписка не скачалась: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("подписка не скачалась: %w", err)
	}
	text := strings.TrimSpace(string(body))
	if !strings.Contains(text, "://") {
		compact := strings.Join(strings.Fields(text), "")
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			if d, err := enc.DecodeString(compact); err == nil {
				text = string(d)
				break
			}
		}
	}
	var out []Server
	skipped := 0
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "vless://") {
			continue
		}
		if s, err := ParseVLESS(line); err == nil {
			out = append(out, s)
		} else {
			skipped++
		}
	}
	if len(out) == 0 {
		if skipped > 0 {
			return nil, bad("в подписке %d ссылок vless://, и ни одну не удалось разобрать", skipped)
		}
		return nil, bad("в подписке нет ссылок vless://")
	}
	return out, nil
}

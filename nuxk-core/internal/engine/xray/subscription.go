package xray

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/engine"
)

// Sub is a fetched 3x-ui subscription: its VLESS servers in the panel's
// order and what the panel says about it in the response headers.
type Sub struct {
	Servers  []Server
	Skipped  int                   // vless:// links that don't parse
	Title    string                // Profile-Title
	Usage    *engine.UpstreamUsage // Subscription-Userinfo
	RefreshS int64                 // Profile-Update-Interval (hours → s)
}

// Key names a server across refreshes: its name and address, not its place
// in the list (a panel may reorder it).
func (s Server) Key() string { return s.Name + "@" + s.Address() }

// Address is host:port.
func (s Server) Address() string { return net.JoinHostPort(s.Host, strconv.Itoa(s.Port)) }

// Public is the server without its secrets.
func (s Server) Public() engine.UpstreamServer {
	return engine.UpstreamServer{Key: s.Key(), Name: s.Name, Address: s.Address(), Security: s.Security, Network: s.Network, Flow: s.Flow}
}

// Pick is the index of the server with this key (or this index, as a
// number); -1 when there's none.
func (sb Sub) Pick(key string) int {
	for i, s := range sb.Servers {
		if s.Key() == key {
			return i
		}
	}
	if n, err := strconv.Atoi(key); err == nil && n >= 0 && n < len(sb.Servers) {
		return n
	}
	return -1
}

// Subscription fetches a 3x-ui subscription. The body is base64 of one link
// per line (plain text is accepted too); links of other protocols are
// skipped. Errors never show the URL: it carries the subscription's secret.
func Subscription(ctx context.Context, client *http.Client, link string) (Sub, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return Sub{}, bad("подписка — это ссылка http(s)://")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Sub{}, bad("подписка — непонятная ссылка")
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Sub{}, fmt.Errorf("подписка не скачалась: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Sub{}, fmt.Errorf("подписка не скачалась: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Sub{}, fmt.Errorf("подписка не скачалась: %w", err)
	}
	sb := Sub{
		Title:    headerText(resp.Header.Get("Profile-Title")),
		Usage:    userinfo(resp.Header.Get("Subscription-Userinfo")),
		RefreshS: hours(resp.Header.Get("Profile-Update-Interval")),
	}
	sc := bufio.NewScanner(strings.NewReader(decodeBody(string(body))))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "vless://") {
			continue
		}
		if s, err := ParseVLESS(line); err == nil {
			sb.Servers = append(sb.Servers, s)
		} else {
			sb.Skipped++
		}
	}
	if len(sb.Servers) == 0 {
		if sb.Skipped > 0 {
			return Sub{}, bad("в подписке %d ссылок vless://, и ни одну не удалось разобрать", sb.Skipped)
		}
		return Sub{}, bad("в подписке нет ссылок vless://")
	}
	return sb, nil
}

func decodeBody(text string) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "://") {
		return text
	}
	compact := strings.Join(strings.Fields(text), "")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if d, err := enc.DecodeString(compact); err == nil {
			return string(d)
		}
	}
	return text
}

// headerText: a plain header value or "base64:…" (3x-ui encodes non-ASCII
// titles that way).
func headerText(v string) string {
	v = strings.TrimSpace(v)
	if b64, ok := strings.CutPrefix(v, "base64:"); ok {
		if d, err := base64.StdEncoding.DecodeString(b64); err == nil {
			v = string(d)
		}
	}
	if len(v) > 120 {
		v = v[:120]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, v)
}

// userinfo parses "upload=1; download=2; total=3; expire=4".
func userinfo(v string) *engine.UpstreamUsage {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var u engine.UpstreamUsage
	seen := false
	for _, part := range strings.Split(v, ";") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil || n < 0 {
			continue
		}
		seen = true
		switch strings.ToLower(k) {
		case "upload":
			u.Upload = n
		case "download":
			u.Download = n
		case "total":
			u.Total = n
		case "expire":
			u.Expire = n
		}
	}
	if !seen {
		return nil
	}
	return &u
}

func hours(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n * 3600
}

package main

// VLESS servers kept on the Pi (the full version): links and 3x-ui
// subscriptions, as many as the person likes. The router never fetches
// them: it gets the one chosen as a single vless:// link through the
// agent's PUT /api/v1/engines/xray/config, so a small router carries one
// config however many subscriptions there are. Links and subscription URLs
// are secrets: kept in controller.json (0600), never served back.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VlessSource is one link or subscription, with its secrets.
type VlessSource struct {
	ID        string      `json:"id"`
	Kind      string      `json:"kind"` // link | subscription
	Name      string      `json:"name,omitempty"`
	URL       string      `json:"url"`             // secret
	Links     []string    `json:"links,omitempty"` // secret: what the subscription gave, vless:// only
	Title     string      `json:"title,omitempty"`
	Usage     *VlessUsage `json:"usage,omitempty"`
	RefreshS  int64       `json:"refresh_s,omitempty"`
	FetchedAt int64       `json:"fetched_at,omitempty"`
	TriedAt   int64       `json:"tried_at,omitempty"` // the last attempt, failed or not
	Skipped   int         `json:"skipped,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// VlessUsage: 3x-ui's subscription-userinfo — bytes; unix seconds; 0 = unlimited / never.
type VlessUsage struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"`
}

// VlessActive is the server the router got last from here.
type VlessActive struct {
	Source string `json:"source"`
	Key    string `json:"key"`
	At     int64  `json:"at"`
}

type VlessBook struct {
	Sources []VlessSource `json:"sources"`
	Active  *VlessActive  `json:"active,omitempty"`
}

// --- what the browser sees: no URLs, no links -----------------------------------------

type VlessServer struct {
	Key      string `json:"key"` // name@host:port — the agent names servers the same way
	Name     string `json:"name"`
	Address  string `json:"address"`
	Security string `json:"security"`
	Network  string `json:"network"`
	Flow     string `json:"flow,omitempty"`
}

type VlessSourceView struct {
	ID        string        `json:"id"`
	Kind      string        `json:"kind"`
	Name      string        `json:"name,omitempty"`
	Title     string        `json:"title,omitempty"`
	Usage     *VlessUsage   `json:"usage,omitempty"`
	RefreshS  int64         `json:"refresh_s,omitempty"`
	FetchedAt int64         `json:"fetched_at,omitempty"`
	Skipped   int           `json:"skipped,omitempty"`
	Error     string        `json:"error,omitempty"`
	Servers   []VlessServer `json:"servers"`
}

type VlessView struct {
	Sources []VlessSourceView `json:"sources"`
	Active  *VlessActive      `json:"active,omitempty"`
}

func (b VlessBook) view() VlessView {
	v := VlessView{Sources: []VlessSourceView{}, Active: b.Active}
	for _, s := range b.Sources {
		sv := VlessSourceView{ID: s.ID, Kind: s.Kind, Name: s.Name, Title: s.Title, Usage: s.Usage, RefreshS: s.RefreshS,
			FetchedAt: s.FetchedAt, Skipped: s.Skipped, Error: s.Error, Servers: []VlessServer{}}
		for _, l := range s.links() {
			if srv, ok := parseVless(l); ok {
				sv.Servers = append(sv.Servers, srv)
			}
		}
		v.Sources = append(v.Sources, sv)
	}
	return v
}

func (s VlessSource) links() []string {
	if s.Kind == "link" {
		return []string{s.URL}
	}
	return s.Links
}

// link is the source's vless:// line for a server key, "" if none.
func (s VlessSource) link(key string) string {
	for _, l := range s.links() {
		if srv, ok := parseVless(l); ok && srv.Key == key {
			return l
		}
	}
	return ""
}

// parseVless reads a link far enough to show it and to name it; the agent
// checks the rest when the link reaches it.
func parseVless(link string) (VlessServer, bool) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "vless" || u.User == nil || u.User.Username() == "" || u.Hostname() == "" || u.Port() == "" {
		return VlessServer{}, false
	}
	q := u.Query()
	nw := q.Get("type")
	if nw == "" || nw == "tcp" {
		nw = "raw"
	}
	sec := q.Get("security")
	if sec == "" {
		sec = "none"
	}
	addr := net.JoinHostPort(u.Hostname(), u.Port())
	return VlessServer{Key: u.Fragment + "@" + addr, Name: u.Fragment, Address: addr, Security: sec, Network: nw, Flow: q.Get("flow")}, true
}

// --- store -------------------------------------------------------------------------------

func (st *Store) Vless() VlessBook {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s.VLESS == nil {
		return VlessBook{}
	}
	b := *st.s.VLESS
	b.Sources = append([]VlessSource(nil), b.Sources...)
	return b
}

func (st *Store) UpdateVless(fn func(*VlessBook) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	b := VlessBook{}
	if st.s.VLESS != nil {
		b = *st.s.VLESS
		b.Sources = append([]VlessSource(nil), b.Sources...)
	}
	if err := fn(&b); err != nil {
		return err
	}
	st.s.VLESS = &b
	return st.save()
}

// --- subscriptions -----------------------------------------------------------------------

type subResult struct {
	links    []string
	skipped  int
	title    string
	usage    *VlessUsage
	refreshS int64
}

var errBadSource = errors.New("bad source")

func badSource(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errBadSource, fmt.Sprintf(format, a...))
}

// fetchSub reads a 3x-ui subscription: base64 (or plain) links, one per
// line, and the panel's headers. Errors never show the URL (its secret).
func fetchSub(ctx context.Context, client *http.Client, raw string) (subResult, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return subResult{}, badSource("подписка — это ссылка http(s)://")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return subResult{}, badSource("подписка — непонятная ссылка")
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return subResult{}, fmt.Errorf("подписка не скачалась: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return subResult{}, fmt.Errorf("подписка не скачалась: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return subResult{}, fmt.Errorf("подписка не скачалась: %w", err)
	}
	r := subResult{title: headerText(resp.Header.Get("Profile-Title")), usage: userinfo(resp.Header.Get("Subscription-Userinfo"))}
	if h, err := strconv.ParseInt(strings.TrimSpace(resp.Header.Get("Profile-Update-Interval")), 10, 64); err == nil && h > 0 {
		r.refreshS = h * 3600
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
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(l, "vless://") {
			continue
		}
		if _, ok := parseVless(l); ok {
			r.links = append(r.links, l)
		} else {
			r.skipped++
		}
	}
	if len(r.links) == 0 {
		return subResult{}, badSource("в подписке нет ссылок vless://, которые удалось разобрать")
	}
	return r, nil
}

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

func userinfo(v string) *VlessUsage {
	var u VlessUsage
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

// --- the API -------------------------------------------------------------------------------

// Vless serves /ctl/v1/vless and keeps the subscriptions fresh.
type Vless struct {
	st   *Store
	ag   *Agent
	http *http.Client
	push *http.Client // to the agent: xray tests and restarts, up to ~20 s
	mu   sync.Mutex   // one change at a time
}

func NewVless(st *Store, ag *Agent) *Vless {
	return &Vless{st: st, ag: ag, http: &http.Client{Timeout: 25 * time.Second}, push: &http.Client{Timeout: 90 * time.Second}}
}

func (v *Vless) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ctl/v1/vless", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, v.st.Vless().view())
	})
	mux.HandleFunc("POST /ctl/v1/vless/sources", v.add)
	mux.HandleFunc("DELETE /ctl/v1/vless/sources/{id}", v.remove)
	mux.HandleFunc("POST /ctl/v1/vless/sources/{id}/refresh", func(w http.ResponseWriter, r *http.Request) {
		v.mu.Lock()
		defer v.mu.Unlock()
		if err := v.refresh(r.Context(), r.PathValue("id")); err != nil {
			v.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, v.st.Vless().view())
	})
	mux.HandleFunc("POST /ctl/v1/vless/use", v.use)
}

var errNoSource = errors.New("no such source or server")

func (v *Vless) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBadSource):
		writeErr(w, http.StatusBadRequest, "bad_source", strings.TrimPrefix(err.Error(), errBadSource.Error()+": "))
	case errors.Is(err, errNoSource):
		writeErr(w, http.StatusNotFound, "no_source", "такого источника или сервера нет — обновите страницу")
	default:
		writeErr(w, http.StatusBadGateway, "vless", err.Error())
	}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (v *Vless) add(w http.ResponseWriter, r *http.Request) {
	var in struct{ Kind, URL, Name string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"kind":"link|subscription","url":"…","name":"…"}`)
		return
	}
	in.URL, in.Name = strings.TrimSpace(in.URL), strings.TrimSpace(in.Name)
	if len(in.Name) > 60 {
		in.Name = in.Name[:60]
	}
	src := VlessSource{ID: newID(), Kind: in.Kind, Name: in.Name, URL: in.URL}
	switch in.Kind {
	case "link":
		if _, ok := parseVless(in.URL); !ok {
			v.fail(w, badSource("это не ссылка vless:// — нужны id, адрес и порт"))
			return
		}
		src.FetchedAt = time.Now().Unix()
	case "subscription":
		res, err := fetchSub(r.Context(), v.http, in.URL)
		if err != nil {
			v.fail(w, err)
			return
		}
		src.apply(res)
	default:
		v.fail(w, badSource("kind — link или subscription"))
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.st.UpdateVless(func(b *VlessBook) error {
		for _, s := range b.Sources {
			if s.URL == src.URL {
				return badSource("этот источник уже есть")
			}
		}
		b.Sources = append(b.Sources, src)
		return nil
	}); err != nil {
		v.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v.st.Vless().view())
}

func (s *VlessSource) apply(res subResult) {
	s.Links, s.Skipped, s.Title, s.Usage, s.RefreshS = res.links, res.skipped, res.title, res.usage, res.refreshS
	s.FetchedAt, s.TriedAt, s.Error = time.Now().Unix(), time.Now().Unix(), ""
}

func (v *Vless) remove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v.mu.Lock()
	defer v.mu.Unlock()
	err := v.st.UpdateVless(func(b *VlessBook) error {
		for i, s := range b.Sources {
			if s.ID == id {
				b.Sources = append(b.Sources[:i], b.Sources[i+1:]...)
				if b.Active != nil && b.Active.Source == id {
					b.Active = nil // the router keeps running it; nothing here names it any more
				}
				return nil
			}
		}
		return errNoSource
	})
	if err != nil {
		v.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v.st.Vless().view())
}

// refresh re-reads one subscription. When it holds the server the router
// runs and that server's settings changed (new keys, another port), the
// router gets the new link; when the server is gone, the router keeps what
// it has and the source says so.
func (v *Vless) refresh(ctx context.Context, id string) error {
	b := v.st.Vless()
	var src *VlessSource
	for i := range b.Sources {
		if b.Sources[i].ID == id {
			src = &b.Sources[i]
		}
	}
	if src == nil {
		return errNoSource
	}
	if src.Kind != "subscription" {
		return nil
	}
	res, err := fetchSub(ctx, v.http, src.URL)
	if err != nil {
		_ = v.st.UpdateVless(func(b *VlessBook) error {
			for i := range b.Sources {
				if b.Sources[i].ID == id {
					b.Sources[i].Error = strings.TrimPrefix(err.Error(), errBadSource.Error()+": ")
					b.Sources[i].TriedAt = time.Now().Unix()
				}
			}
			return nil
		})
		return err
	}
	old := *src
	src.apply(res)
	repush := ""
	if b.Active != nil && b.Active.Source == id {
		now, was := src.link(b.Active.Key), old.link(b.Active.Key)
		switch {
		case now == "":
			src.Error = "сервера, который сейчас на роутере, больше нет в подписке — роутер работает на прежнем; выберите другой"
		case now != was:
			repush = now
		}
	}
	if err := v.st.UpdateVless(func(bk *VlessBook) error {
		for i := range bk.Sources {
			if bk.Sources[i].ID == id {
				bk.Sources[i] = *src
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if repush != "" {
		if err := v.send(ctx, repush); err != nil {
			return fmt.Errorf("подписка обновилась, но роутер не принял новые настройки сервера: %w", err)
		}
		slog.Info("vless: the active server changed in its subscription; the router got it", "source", id)
	}
	return nil
}

func (v *Vless) use(w http.ResponseWriter, r *http.Request) {
	var in struct{ Source, Server string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"source":"…","server":"<key>"}`)
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	link := ""
	for _, s := range v.st.Vless().Sources {
		if s.ID == in.Source {
			link = s.link(in.Server)
		}
	}
	if link == "" {
		v.fail(w, errNoSource)
		return
	}
	if err := v.send(r.Context(), link); err != nil {
		v.fail(w, err)
		return
	}
	_ = v.st.UpdateVless(func(b *VlessBook) error {
		b.Active = &VlessActive{Source: in.Source, Key: in.Server, At: time.Now().Unix()}
		return nil
	})
	writeJSON(w, http.StatusOK, v.st.Vless().view())
}

// send hands one link to the router's xray, through the agent.
func (v *Vless) send(ctx context.Context, link string) error {
	base, token := v.ag.Ref()
	if base == "" {
		return errors.New("роутер ещё не подключён — пройдите настройку")
	}
	body, _ := json.Marshal(map[string]string{"vless_uri": link})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/api/v1/engines/xray/config", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.push.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("роутер не ответил: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var e struct {
		Error struct{ Message string } `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
	if resp.StatusCode == http.StatusBadRequest {
		return badSource("%s", e.Error.Message)
	}
	return fmt.Errorf("роутер: HTTP %d %s", resp.StatusCode, e.Error.Message)
}

// Run re-reads the subscriptions when due: as often as the panel asks
// (Profile-Update-Interval), within [1 h, 24 h]; 12 h when it doesn't say.
func (v *Vless) Run(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, s := range v.st.Vless().Sources {
				if s.Kind != "subscription" {
					continue
				}
				every := 12 * time.Hour
				if s.RefreshS > 0 {
					every = min(max(time.Duration(s.RefreshS)*time.Second, time.Hour), 24*time.Hour)
				}
				if now.Sub(time.Unix(max(s.FetchedAt, s.TriedAt), 0)) < every {
					continue
				}
				v.mu.Lock()
				if err := v.refresh(ctx, s.ID); err != nil {
					slog.Warn("vless: subscription refresh", "source", s.ID, "err", err)
				}
				v.mu.Unlock()
			}
		}
	}
}

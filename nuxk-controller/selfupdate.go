package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The controller's own update and «Обновить всё»: the router first (its own
// update from the panel, through the agent's API), then this controller.
//
// A container can't replace itself, and it isn't given Docker: the panel
// leaves a request in UPDATE_DIR (~/nuxk/update on the Pi, mounted here) and
// the Pi's nuxk-update.path — installed by nuxk-full.sh, once, with sudo —
// runs `nuxk-full.sh panel-update` for it. That script checks the release's
// signature, swaps the container and puts the old one back if the new one
// doesn't answer, reporting into UPDATE_DIR/status. The worst a request can
// do is ask for a newer signed release: the script takes nothing else.

const (
	requestFile = "inbox/request"
	helperFile  = "helper"          // nuxk-full.sh: the unit is installed
	hostStatus  = "status"          // the script's reports
	hostLog     = "update.log"      // its output
	allFile     = "update-all.json" // in DATA_DIR: «Обновить всё», across this controller's restart
	handoffFile = "sessions.handoff"
	pickupWait  = time.Minute // a request the helper hasn't taken by then: it isn't there
)

// HostRun: the controller's update, as nuxk-full.sh reported it.
type HostRun struct {
	State     string   `json:"state"` // running | done | failed | rolled_back | interrupted
	From      string   `json:"from"`
	To        string   `json:"to"`
	Message   string   `json:"message,omitempty"`
	StartedAt int64    `json:"started_at"`
	At        int64    `json:"at"`
	Log       []string `json:"log"`
}

// AllRun: «Обновить всё» — which step it's on.
type AllRun struct {
	State     string `json:"state"` // running | done | failed
	Version   string `json:"version"`
	Step      string `json:"step"` // router | controller | done
	Message   string `json:"message,omitempty"`
	StartedAt int64  `json:"started_at"`
	At        int64  `json:"at"`
}

// SelfUpdate is GET /ctl/v1/update.
type SelfUpdate struct {
	Current   string   `json:"current"`
	Latest    string   `json:"latest,omitempty"` // the newest release the router found (its channel)
	Router    string   `json:"router,omitempty"` // the router's version
	Available bool     `json:"available"`        // latest is newer than this controller
	CanApply  bool     `json:"can_apply"`
	Cannot    string   `json:"cannot,omitempty"`
	Run       *HostRun `json:"run"`
	All       *AllRun  `json:"all"`
}

var (
	errUpdBusy    = errors.New("обновление уже идёт")
	errUpdStale   = errors.New("эта версия не новее — проверьте обновления ещё раз")
	errUpdCannot  = errors.New("обновить контроллер отсюда нельзя")
	errUpdNothing = errors.New("и роутер, и контроллер уже на этой версии")
)

// agentUpdate: what the agent's GET /api/v1/update says (the part used here).
type agentUpdate struct {
	Current string `json:"current"`
	Latest  *struct {
		Version string `json:"version"`
	} `json:"latest"`
	Run *struct {
		State   string `json:"state"`
		To      string `json:"to"`
		Message string `json:"message"`
	} `json:"run"`
}

type SelfUpdater struct {
	dir     string // UPDATE_DIR
	data    string // DATA_DIR
	current string
	ag      *Agent
	ses     *Sessions
	now     func() time.Time
	poll    time.Duration // the router's update: how often to look

	mu      sync.Mutex
	running bool // «Обновить всё» is working here
}

func NewSelfUpdater(dir, data, current string, ag *Agent, ses *Sessions) *SelfUpdater {
	return &SelfUpdater{dir: dir, data: data, current: current, ag: ag, ses: ses, now: time.Now, poll: 3 * time.Second}
}

// Resume: after this controller started — the end of «Обновить всё» (the new
// controller is this one), or the router's step it was waiting on.
func (u *SelfUpdater) Resume() {
	all := u.readAll()
	if all == nil || all.State != "running" {
		return
	}
	if all.Step == "router" {
		u.mu.Lock()
		u.running = true
		u.mu.Unlock()
		go u.runAll(all.Version)
	}
}

func (u *SelfUpdater) cannot() string {
	if st, err := os.Stat(u.dir); err != nil || !st.IsDir() {
		return "контроллер запущен не установщиком nuxk-full.sh — обновляйте тем же способом, что ставили"
	}
	if _, err := os.Stat(filepath.Join(u.dir, helperFile)); err != nil {
		return "на Pi нет помощника обновления — один раз выполните в терминале Pi: sh ~/nuxk/nuxk-full.sh update (спросит пароль sudo)"
	}
	return ""
}

// Status: this controller, the newest release (as the router's update found
// it), the last controller update, «Обновить всё».
func (u *SelfUpdater) Status(ctx context.Context) SelfUpdate {
	s := SelfUpdate{Current: u.current, Run: u.readRun(), All: u.reconcile()}
	var au agentUpdate
	if err := u.agentGet(ctx, &au); err == nil {
		s.Router = au.Current
		if au.Latest != nil {
			s.Latest = au.Latest.Version
		}
	}
	s.Available = s.Latest != "" && Newer(s.Latest, s.Current)
	s.Cannot = u.cannot()
	s.CanApply = s.Cannot == ""
	return s
}

// Start: this controller alone, to version.
func (u *SelfUpdater) Start(ctx context.Context, version string) error {
	if why := u.cannot(); why != "" {
		return fmt.Errorf("%w: %s", errUpdCannot, why)
	}
	if u.busy() {
		return errUpdBusy
	}
	var au agentUpdate
	if err := u.agentGet(ctx, &au); err != nil || au.Latest == nil || au.Latest.Version != version || !Newer(version, u.current) {
		return errUpdStale
	}
	return u.request(version)
}

// StartAll: the router, then this controller, to version — each only if
// it's older. Answers at once; GET /ctl/v1/update follows it.
func (u *SelfUpdater) StartAll(ctx context.Context, version string) error {
	if u.busy() {
		return errUpdBusy
	}
	var au agentUpdate
	if err := u.agentGet(ctx, &au); err != nil {
		return fmt.Errorf("роутер не ответил: %w", err)
	}
	if au.Latest == nil || au.Latest.Version != version {
		return errUpdStale
	}
	router, ctl := Newer(version, au.Current), Newer(version, u.current)
	if !router && !ctl {
		return errUpdNothing
	}
	if why := u.cannot(); ctl && why != "" {
		return fmt.Errorf("%w: %s", errUpdCannot, why)
	}
	now := u.now().Unix()
	u.mu.Lock()
	u.running = true
	u.mu.Unlock()
	if err := u.writeAll(AllRun{State: "running", Version: version, Step: "router", Message: "Обновляю роутер", StartedAt: now, At: now}); err != nil {
		u.mu.Lock()
		u.running = false
		u.mu.Unlock()
		return err
	}
	go u.runAll(version)
	return nil
}

func (u *SelfUpdater) runAll(version string) {
	defer func() {
		u.mu.Lock()
		u.running = false
		u.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	fail := func(msg string) {
		u.step(func(a *AllRun) { a.State, a.Message = "failed", msg })
		slog.Warn("update all", "err", msg)
	}
	// 1. the router: its own update, as from the panel, until it reports
	var au agentUpdate
	if err := u.agentGet(ctx, &au); err != nil {
		fail("роутер не ответил: " + err.Error())
		return
	}
	if Newer(version, au.Current) {
		if au.Run == nil || au.Run.State != "running" || au.Run.To != version {
			if err := u.agentPost(ctx, "/api/v1/update", map[string]string{"version": version}); err != nil {
				fail("роутер не начал обновление: " + err.Error())
				return
			}
		}
		u.step(func(a *AllRun) {
			a.Message = "Роутер обновляется — агент перезапускается"
		})
		for {
			select {
			case <-ctx.Done():
				fail("роутер обновляется дольше 20 минут — посмотрите «Обновления» роутера")
				return
			case <-time.After(u.poll):
			}
			var st agentUpdate
			if u.agentGet(ctx, &st) != nil {
				continue // restarting
			}
			if st.Current == version {
				break
			}
			if st.Run != nil && st.Run.To == version && st.Run.State != "running" {
				fail("роутер не обновился: " + st.Run.Message)
				return
			}
		}
	}
	// 2. this controller: a request for the Pi's helper; the next controller
	// finishes the record
	if !Newer(version, u.current) {
		u.step(func(a *AllRun) {
			a.State, a.Step, a.Message = "done", "done", "Всё обновлено до "+version
		})
		return
	}
	u.step(func(a *AllRun) {
		a.Step, a.Message = "controller", "Pi обновляет контроллер — панель пропадёт примерно на минуту"
	})
	if err := u.request(version); err != nil {
		fail("контроллер: " + err.Error())
	}
}

// request leaves the request for the Pi's helper; the logins go to the next
// controller.
func (u *SelfUpdater) request(version string) error {
	if !Valid(version) {
		return errUpdStale
	}
	if u.ses != nil {
		if err := u.ses.Handoff(filepath.Join(u.data, handoffFile)); err != nil {
			slog.Warn("update: logins not handed over", "err", err)
		}
	}
	p := filepath.Join(u.dir, requestFile)
	body := fmt.Sprintf("version %s\nat %d\n", version, u.now().Unix())
	if err := os.WriteFile(p+".tmp", []byte(body), 0o644); err != nil {
		return fmt.Errorf("запрос на Pi не записался: %w", err)
	}
	return os.Rename(p+".tmp", p)
}

func (u *SelfUpdater) busy() bool {
	u.mu.Lock()
	r := u.running
	u.mu.Unlock()
	if r {
		return true
	}
	if a := u.reconcile(); a != nil && a.State == "running" {
		return true
	}
	run := u.readRun()
	return run != nil && run.State == "running"
}

// readRun: the controller's last update, from the Pi's reports — or the
// request itself, while the helper hasn't taken it.
func (u *SelfUpdater) readRun() *HostRun {
	if st, err := os.Stat(filepath.Join(u.dir, requestFile)); err == nil {
		r := &HostRun{State: "running", Message: "Запрос передан помощнику на Pi", StartedAt: st.ModTime().Unix(), At: st.ModTime().Unix(), Log: []string{}}
		if u.now().Sub(st.ModTime()) > pickupWait {
			r.State = "failed"
			r.Message = "помощник обновления на Pi не забрал запрос за минуту — на Pi: systemctl status nuxk-update.path"
		}
		return r
	}
	f, err := os.Open(filepath.Join(u.dir, hostStatus))
	if err != nil {
		return nil
	}
	defer f.Close()
	kv := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		kv[k] = v
	}
	r := &HostRun{State: kv["state"], From: kv["from"], To: kv["to"], Message: strings.TrimSpace(kv["message"]),
		StartedAt: unixOf(kv["started"]), At: unixOf(kv["at"]), Log: tailOf(filepath.Join(u.dir, hostLog), 40)}
	if r.State == "" {
		return nil
	}
	// the script's pid is the Pi's, not seen from here: a run silent for 15
	// minutes was cut short
	if r.State == "running" && u.now().Unix()-r.At > 900 {
		r.State, r.Message = "interrupted", "прервалось: "+r.Message
	}
	return r
}

// reconcile: «Обновить всё» brought up to date — done when this controller
// is the version it asked for, failed when the Pi says the update didn't go.
func (u *SelfUpdater) reconcile() *AllRun {
	u.mu.Lock()
	defer u.mu.Unlock()
	a := u.readAll()
	if a == nil || a.State != "running" || a.Step != "controller" {
		return a
	}
	switch run := u.readRun(); {
	case u.current == a.Version:
		a.State, a.Step, a.Message = "done", "done", "Всё обновлено до "+a.Version
	case run != nil && run.State != "running" && (run.To == a.Version || run.To == ""):
		a.State, a.Message = "failed", "контроллер не обновился: "+run.Message
	default:
		return a
	}
	a.At = u.now().Unix()
	_ = u.writeAll(*a)
	return a
}

func (u *SelfUpdater) step(f func(*AllRun)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	a := u.readAll()
	if a == nil {
		return
	}
	f(a)
	a.At = u.now().Unix()
	_ = u.writeAll(*a)
}

func (u *SelfUpdater) readAll() *AllRun {
	b, err := os.ReadFile(filepath.Join(u.data, allFile))
	if err != nil {
		return nil
	}
	var a AllRun
	if json.Unmarshal(b, &a) != nil || a.State == "" {
		return nil
	}
	return &a
}

func (u *SelfUpdater) writeAll(a AllRun) error {
	b, _ := json.Marshal(a)
	p := filepath.Join(u.data, allFile)
	if err := os.WriteFile(p+".tmp", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func (u *SelfUpdater) agentGet(ctx context.Context, v any) error {
	if base, _ := u.ag.Ref(); base == "" {
		return errors.New("роутер не подключён")
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return u.ag.get(cctx, "/api/v1/update", v)
}

// agentPost: the agent's API with the controller's token; its error message
// when it answers with one.
func (u *SelfUpdater) agentPost(ctx context.Context, path string, in any) error {
	base, tok := u.ag.Ref()
	b, _ := json.Marshal(in)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.ag.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 {
		return nil
	}
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		return errors.New(e.Error.Message)
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

// routes: GET /ctl/v1/update; POST /ctl/v1/update {version} (this
// controller); POST /ctl/v1/update/all {version}.
func (u *SelfUpdater) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ctl/v1/update", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, u.Status(r.Context()))
	})
	start := func(all bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				Version string `json:"version"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || !Valid(in.Version) {
				writeErr(w, http.StatusBadRequest, "bad_body", `want {"version":"X.Y.Z"}`)
				return
			}
			var err error
			if all {
				err = u.StartAll(r.Context(), in.Version)
			} else {
				err = u.Start(r.Context(), in.Version)
			}
			switch {
			case errors.Is(err, errUpdBusy):
				writeErr(w, http.StatusConflict, "update_busy", err.Error())
			case errors.Is(err, errUpdStale), errors.Is(err, errUpdNothing):
				writeErr(w, http.StatusConflict, "update_stale", err.Error())
			case errors.Is(err, errUpdCannot):
				writeErr(w, http.StatusServiceUnavailable, "update_unavailable", err.Error())
			case err != nil:
				writeErr(w, http.StatusBadGateway, "update_failed", err.Error())
			default:
				writeJSON(w, http.StatusAccepted, u.Status(r.Context()))
			}
		}
	}
	mux.HandleFunc("POST /ctl/v1/update", start(false))
	mux.HandleFunc("POST /ctl/v1/update/all", start(true))
}

func unixOf(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func tailOf(path string, n int) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}
	if len(b) > 64<<10 {
		b = b[len(b)-64<<10:]
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	return lines
}

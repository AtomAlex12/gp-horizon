// Package update: is there a newer nuxk Horizon release, and the router
// updated to it from the panel.
//
// The agent only looks (GitHub's list of releases, once a day) and starts the
// router's own `nuxk update` (install/nuxk-lite.sh) in the background. That
// script does the work, as from SSH: the release's files checked against its
// SHA256SUMS, the SHA256SUMS signature checked by this very agent (a new one
// can't vouch for itself), the agent restarted and asked if it answers, the
// old one put back if it doesn't. It stops and restarts this agent on the
// way, so it reports into a file (NUXK_STATUS) the next agent reads.
package update

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultRepo is where releases come from (UPDATE_REPO in nuxk.conf).
const DefaultRepo = "AtomAlex12/nuxk-horizon"

const (
	ChannelStable = "stable" // releases only
	ChannelBeta   = "beta"   // pre-releases too

	statusFile = "update-run"
	logFile    = "update.log"
	notesMax   = 16 << 10
	logLines   = 40
)

// Settings: set in the panel, kept in the state dir.
type Settings struct {
	Check   bool   `json:"check"`   // look for a new release once a day
	Channel string `json:"channel"` // stable | beta
}

// Release is the newest release GitHub has for the channel.
type Release struct {
	Version     string `json:"version"`
	Name        string `json:"name,omitempty"`
	Notes       string `json:"notes,omitempty"` // the release's text (from CHANGELOG), Markdown
	URL         string `json:"url"`
	PublishedAt int64  `json:"published_at"` // unix seconds
	Prerelease  bool   `json:"prerelease"`
}

// Run is the last update (or component install) started from the panel, as
// its script reported it.
type Run struct {
	State     string   `json:"state"`          // running | done | failed | rolled_back | interrupted
	Task      string   `json:"task,omitempty"` // a component install: its id
	From      string   `json:"from"`
	To        string   `json:"to"`
	Message   string   `json:"message,omitempty"`
	StartedAt int64    `json:"started_at"` // unix seconds
	At        int64    `json:"at"`         // the last report
	Log       []string `json:"log"`        // the script's last lines
}

// Status is GET /api/v1/update.
type Status struct {
	Current    string   `json:"current"`
	Settings   Settings `json:"settings"`
	Latest     *Release `json:"latest"`
	Available  bool     `json:"available"`            // latest is newer than current
	CheckedAt  int64    `json:"checked_at,omitempty"` // unix seconds; 0 = not yet
	CheckError string   `json:"check_error,omitempty"`
	CanApply   bool     `json:"can_apply"`
	Cannot     string   `json:"cannot,omitempty"` // why not, when it can't
	Run        *Run     `json:"run"`
}

var (
	ErrBusy           = errors.New("обновление уже идёт")
	ErrNotNewer       = errors.New("эта версия не новее установленной")
	ErrUnknownVersion = errors.New("такой версии нет среди найденных — проверьте обновления ещё раз")
	ErrCannot         = errors.New("обновить отсюда нельзя")
	ErrBadSettings    = errors.New("канал — stable или beta")
)

// Store keeps the settings (state.Store).
type Store interface {
	LoadJSON(name string, v any) error
	SaveJSON(name string, v any) error
}

type Options struct {
	Current string // this agent's version
	Repo    string // owner/name on GitHub
	Command string // the router's `nuxk`; missing = no updates from the panel
	Dir     string // the run's status and log (the state dir)
	API     string // GitHub API base, https://api.github.com
	BaseURL string // UPDATE_BASE_URL: a mirror of the releases' files; "" = GitHub
	Every   time.Duration
	// Handoff runs just before the update starts: the logins go to the next
	// agent (auth.Guard.Handoff). Its failure only costs a login.
	Handoff func() error
	// Have: what's on the router of each component (the agent's engines and
	// DNS know); nil = no components from the panel.
	Have func(id string) Presence
}

type Updater struct {
	o     Options
	st    Store
	http  *http.Client
	now   func() time.Time
	alive func(pid int) bool
	first time.Duration // the first check after start

	mu        sync.Mutex
	set       Settings
	latest    *Release
	checkedAt time.Time
	checkErr  string
	checking  time.Time // a manual check: not more often than once a minute
}

func New(o Options, st Store) *Updater {
	if o.Repo == "" {
		o.Repo = DefaultRepo
	}
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Every <= 0 {
		o.Every = 24 * time.Hour
	}
	u := &Updater{
		o: o, st: st,
		http:  &http.Client{Timeout: 20 * time.Second},
		now:   time.Now,
		alive: processAlive,
		first: 2 * time.Minute,
		set:   Settings{Check: true, Channel: ChannelStable},
	}
	if st != nil {
		_ = st.LoadJSON("update", &u.set)
	}
	if u.set.Channel != ChannelBeta {
		u.set.Channel = ChannelStable
	}
	return u
}

// Run looks for a new release a couple of minutes after start, then once a
// day (an hour after a failed look).
func (u *Updater) Run(ctx context.Context) {
	t := time.NewTimer(u.firstLook())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		next := u.o.Every
		if u.Settings().Check {
			if st := u.Check(ctx); st.CheckError != "" {
				next = time.Hour
			}
		}
		t.Reset(next)
	}
}

// firstLook: when to look after start. Just restarted by an update (its
// script is still checking on this very agent): in a few seconds — the panel
// shows the result, and what's the newest now, not "not checked yet".
func (u *Updater) firstLook() time.Duration {
	if r := u.readRun(); r != nil && u.now().Unix()-r.At < 600 {
		return 5 * time.Second
	}
	return u.first
}

func (u *Updater) Settings() Settings {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.set
}

// SetSettings stores the panel's choice. Another channel forgets what the
// old one found.
func (u *Updater) SetSettings(s Settings) (Status, error) {
	if s.Channel != ChannelStable && s.Channel != ChannelBeta {
		return u.Status(), ErrBadSettings
	}
	u.mu.Lock()
	if s.Channel != u.set.Channel {
		u.latest, u.checkedAt, u.checkErr = nil, time.Time{}, ""
	}
	u.set = s
	var err error
	if u.st != nil {
		err = u.st.SaveJSON("update", s)
	}
	u.mu.Unlock()
	return u.Status(), err
}

// CheckNow is the panel's «Проверить»: at most once a minute goes to GitHub.
func (u *Updater) CheckNow(ctx context.Context) Status {
	u.mu.Lock()
	recent := u.now().Sub(u.checking) < time.Minute
	if !recent {
		u.checking = u.now()
	}
	u.mu.Unlock()
	if recent {
		return u.Status()
	}
	return u.Check(ctx)
}

// Check asks GitHub for the newest release of the channel. A failed look
// keeps what an earlier one found.
func (u *Updater) Check(ctx context.Context) Status {
	rel, err := u.fetch(ctx, u.Settings().Channel)
	u.mu.Lock()
	u.checkedAt = u.now()
	if err != nil {
		u.checkErr = err.Error()
	} else {
		u.latest, u.checkErr = rel, ""
	}
	u.mu.Unlock()
	return u.Status()
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

func (u *Updater) fetch(ctx context.Context, channel string) (*Release, error) {
	// the list, not /releases/latest: the beta channel needs pre-releases,
	// and the newest by version is safer than the newest by date
	url := strings.TrimRight(u.o.API, "/") + "/repos/" + u.o.Repo + "/releases?per_page=30"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "nuxk-core/"+u.o.Current)
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("нет связи с GitHub: %v", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("на GitHub нет репозитория %s (или он закрыт)", u.o.Repo)
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("GitHub просит спрашивать реже — проверю позже")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub ответил %s", resp.Status)
	}
	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("не понял ответ GitHub: %v", err)
	}
	var best *ghRelease
	for i := range list {
		r := &list[i]
		if r.Draft || !Valid(r.TagName) || (r.Prerelease && channel != ChannelBeta) {
			continue
		}
		if best == nil || Newer(r.TagName, best.TagName) {
			best = r
		}
	}
	if best == nil {
		return nil, errors.New("в репозитории пока нет релизов")
	}
	notes := best.Body
	if len(notes) > notesMax {
		notes = notes[:notesMax] + "\n…"
	}
	return &Release{
		Version: strings.TrimPrefix(best.TagName, "v"), Name: best.Name, Notes: notes,
		URL: best.HTMLURL, PublishedAt: best.PublishedAt.Unix(), Prerelease: best.Prerelease,
	}, nil
}

func (u *Updater) Status() Status {
	u.mu.Lock()
	s := Status{Current: u.o.Current, Settings: u.set, Latest: u.latest, CheckError: u.checkErr}
	if !u.checkedAt.IsZero() {
		s.CheckedAt = u.checkedAt.Unix()
	}
	u.mu.Unlock()
	s.Available = s.Latest != nil && Newer(s.Latest.Version, s.Current)
	s.Cannot = u.cannot()
	s.CanApply = s.Cannot == ""
	s.Run = u.readRun()
	return s
}

// cannot: why the panel can't update this box ("" = it can).
func (u *Updater) cannot() string {
	if u.o.Command == "" {
		return "обновление из панели выключено (UPDATE_COMMAND пуст в nuxk.conf)"
	}
	if _, err := os.Stat(u.o.Command); err != nil {
		return "на этом узле нет команды nuxk (" + u.o.Command + ") — nuxk поставлен не установщиком nuxk-lite; обновляйте тем же способом, что ставили"
	}
	return ""
}

// Start runs `nuxk update --yes` for version in the background and returns
// at once: the script reports into the status file.
func (u *Updater) Start(version string) (Status, error) {
	u.mu.Lock()
	err := u.startLocked(version)
	u.mu.Unlock()
	return u.Status(), err
}

func (u *Updater) startLocked(version string) error {
	if why := u.cannot(); why != "" {
		return fmt.Errorf("%w: %s", ErrCannot, why)
	}
	if u.busy() {
		return ErrBusy
	}
	if u.latest == nil || version != u.latest.Version {
		return ErrUnknownVersion
	}
	if !Newer(version, u.o.Current) {
		return ErrNotNewer
	}
	return u.spawn(script{
		args: []string{"update", "--yes"}, status: statusFile, log: logFile,
		first: fmt.Sprintf("from %s\nto %s\nmessage Запускаю обновление\n", u.o.Current, version),
		env:   []string{"NUXK_VERSION=" + version, "NUXK_FROM=" + u.o.Current},
	})
}

// script: one run of the router's `nuxk` from the panel.
type script struct {
	args   []string // nuxk's arguments
	status string   // its report file in Dir
	log    string   // its output in Dir
	first  string   // the status until the script's own first report
	env    []string // more for it, NUXK_*
}

// spawn starts `sh nuxk ARGS` in the background, apart from this agent (it
// restarts the agent on the way) and returns at once; the script reports
// into its status file.
func (u *Updater) spawn(sc script) error {
	if err := os.MkdirAll(u.o.Dir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(u.o.Dir, sc.log), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	started := strconv.FormatInt(u.now().Unix(), 10)
	status := filepath.Join(u.o.Dir, sc.status)
	// the script's first report replaces this within a second; pid 0 until then
	st := "state running\npid 0\nstarted " + started + "\nat " + started + "\n" + sc.first
	if err := writeFile(status, st); err != nil {
		logf.Close()
		return err
	}
	if u.o.Handoff != nil {
		if err := u.o.Handoff(); err != nil {
			slog.Warn("update: logins not handed over", "err", err)
		}
	}
	cmd := exec.Command("sh", append([]string{u.o.Command}, sc.args...)...)
	cmd.Env = append(cleanEnv(os.Environ()), "NUXK_STATUS="+status, "NUXK_STARTED="+started, "NO_COLOR=1")
	cmd.Env = append(cmd.Env, sc.env...)
	if u.o.BaseURL != "" {
		cmd.Env = append(cmd.Env, "NUXK_BASE_URL="+u.o.BaseURL)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		logf.Close()
		_ = writeFile(status, strings.Replace(st, "state running", "state failed", 1)+"message не запустилась команда nuxk: "+err.Error()+"\n")
		return err
	}
	go func() {
		_ = cmd.Wait()
		logf.Close()
	}()
	return nil
}

// busy: an update or a component install is going — one at a time, both
// change the router and restart the agent.
func (u *Updater) busy() bool {
	for _, f := range []string{statusFile, componentStatus} {
		if r := u.readRunAt(f, ""); r != nil && r.State == "running" {
			return true
		}
	}
	return false
}

func (u *Updater) statusPath() string { return filepath.Join(u.o.Dir, statusFile) }

// cleanEnv: the agent's environment without the installer's variables. An
// agent (re)started by nuxk-lite.sh may have inherited them — a mirror's URL,
// «already self-updated» — and the next update would follow them: the stale
// script, the mirror long gone. NUXK_ROOT (a test root) is kept.
func cleanEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "NUXK_") && !strings.HasPrefix(kv, "NUXK_ROOT=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// readRun: the last update from its status file.
func (u *Updater) readRun() *Run { return u.readRunAt(statusFile, logFile) }

// readRunAt: a run from its status file (and the tail of its log, if named);
// a "running" one whose script is gone was cut short (a reboot, a power cut).
func (u *Updater) readRunAt(status, log string) *Run {
	f, err := os.Open(filepath.Join(u.o.Dir, status))
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
	r := &Run{State: kv["state"], Task: kv["task"], From: kv["from"], To: kv["to"], Message: strings.TrimSpace(kv["message"]),
		StartedAt: unix(kv["started"]), At: unix(kv["at"])}
	if r.State == "" {
		return nil
	}
	if r.State == "running" {
		pid, _ := strconv.Atoi(kv["pid"])
		gone := pid > 0 && !u.alive(pid)
		stuck := pid == 0 && u.now().Unix()-r.At > 120
		if gone || stuck {
			r.State = "interrupted"
			r.Message = "прервалось (перезагрузка или сбой): " + r.Message
		}
	}
	r.Log = []string{}
	if log != "" {
		r.Log = tailLines(filepath.Join(u.o.Dir, log), logLines)
	}
	return r
}

func unix(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func tailLines(path string, n int) []string {
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

func writeFile(path, s string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(s), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

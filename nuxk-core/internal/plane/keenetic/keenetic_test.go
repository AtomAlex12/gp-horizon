package keenetic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"nuxk.dev/horizon/core/internal/plane"
)

// fakeRouter emulates the KeeneticOS RCI surface the plane uses, with the
// answer shapes and status codes seen on the real router (25.09).
type fakeRouter struct {
	mu      sync.Mutex
	release string
	groups  map[string][]string
	descr   map[string]string
	routes  []plane.Route
	posts   int

	chain   []string // NUXK_V6_DENY rules
	chainOK bool
	jump    bool
}

func newFake() *fakeRouter {
	return &fakeRouter{
		release: "5.01.C.6.0-1",
		groups:  map[string][]string{"domain-list0": {"openai.com", "chatgpt.com"}, "domain-list1": {"instagram.com"}},
		descr:   map[string]string{"domain-list0": "AI", "domain-list1": "Meta"},
		routes: []plane.Route{
			{Group: "domain-list0", Interface: "Wireguard1", Auto: true, Index: "057339b2"},
			{Group: "domain-list1", Interface: "Wireguard1", Auto: true, Index: "2eea6921"},
		},
	}
}

func msg(code, text string) map[string]any {
	return map[string]any{"status": []any{map[string]any{"status": "message", "code": code, "message": text}}}
}
func errStatus(text string) map[string]any {
	return map[string]any{"status": []any{map[string]any{"status": "error", "code": "7405600", "message": text}}}
}

func (f *fakeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	enc := json.NewEncoder(w)
	if r.Method == http.MethodGet {
		switch strings.TrimPrefix(r.URL.Path, "/rci/") {
		case "show/version":
			enc.Encode(map[string]string{"release": f.release})
		case "object-group/fqdn":
			out := map[string]any{}
			for g, d := range f.groups {
				var inc []map[string]string
				for _, a := range d {
					inc = append(inc, map[string]string{"address": a})
				}
				out[g] = map[string]any{"description": f.descr[g], "include": inc}
			}
			enc.Encode(out)
		case "show/interface":
			enc.Encode(map[string]any{"Wireguard1": map[string]any{"type": "Wireguard"}, "GigabitEthernet1": map[string]any{}})
		case "dns-proxy":
			enc.Encode(map[string]any{"route": f.routes, "https": map[string]any{}})
		default:
			w.WriteHeader(404)
		}
		return
	}
	f.posts++
	var body map[string]map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	if og, ok := body["object-group"]; ok {
		fq := og["fqdn"].(map[string]any)
		for name, v := range fq {
			obj := v.(map[string]any)
			if no, _ := obj["no"].(bool); no {
				for _, rt := range f.routes {
					if rt.Group == name {
						enc.Encode(map[string]any{"object-group": map[string]any{"fqdn": map[string]any{name: errStatus("group is in use by a DNS route")}}})
						return
					}
				}
				delete(f.groups, name)
				enc.Encode(map[string]any{"object-group": map[string]any{"fqdn": map[string]any{name: msg("101581100", "group removed.")}}})
				return
			}
			if _, ok := f.groups[name]; !ok {
				f.groups[name] = nil
			}
			if d, ok := obj["description"].(string); ok {
				f.descr[name] = d
			}
			for _, e := range asList(obj["include"]) {
				m := e.(map[string]any)
				addr := m["address"].(string)
				if no, _ := m["no"].(bool); no {
					f.groups[name] = remove(f.groups[name], addr)
				} else if !contains(f.groups[name], addr) {
					f.groups[name] = append(f.groups[name], addr)
				}
			}
			sort.Strings(f.groups[name])
			enc.Encode(map[string]any{"object-group": map[string]any{"fqdn": map[string]any{name: msg("101581000", "group created.")}}})
		}
		return
	}
	if dp, ok := body["dns-proxy"]; ok {
		rt := dp["route"].(map[string]any)
		g, i := rt["group"].(string), rt["interface"].(string)
		if no, _ := rt["no"].(bool); no {
			for k, x := range f.routes {
				if x.Group == g && x.Interface == i {
					f.routes = append(f.routes[:k], f.routes[k+1:]...)
					enc.Encode(map[string]any{"dns-proxy": map[string]any{"route": msg("4456848", "deleted the DNS route rule.")}})
					return
				}
			}
			enc.Encode(map[string]any{"dns-proxy": map[string]any{"route": errStatus("no such route")}})
			return
		}
		if _, ok := f.groups[g]; !ok {
			enc.Encode(map[string]any{"dns-proxy": map[string]any{"route": errStatus("unknown object-group")}})
			return
		}
		auto, _ := rt["auto"].(bool)
		f.routes = append(f.routes, plane.Route{Group: g, Interface: i, Auto: auto, Index: "new"})
		enc.Encode(map[string]any{"dns-proxy": map[string]any{"route": msg("4456748", "added the DNS route.")}})
	}
}

func asList(v any) []any { l, _ := v.([]any); return l }
func contains(s []string, x string) bool {
	for _, y := range s {
		if y == x {
			return true
		}
	}
	return false
}
func remove(s []string, x string) []string {
	var out []string
	for _, y := range s {
		if y != x {
			out = append(out, y)
		}
	}
	return out
}

// run fakes ipset/ip6tables: a set exists per v4/v6 for every group.
func (f *fakeRouter) run(_ context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := strings.Join(args, " ")
	switch {
	case name == "ipset" && a == "list -n":
		var out []string
		for g := range f.groups {
			out = append(out, "_NDM_OGDN_4_@"+g, "_NDM_OGDN_6_@"+g)
		}
		return strings.Join(out, "\n") + "\n", nil
	case name == "ip6tables" && a == "-w -S "+V6Chain:
		if !f.chainOK {
			return "", errFake
		}
		return "-N " + V6Chain + "\n" + strings.Join(f.chain, "\n"), nil
	case a == "-w -N "+V6Chain:
		f.chainOK = true
	case a == "-w -F "+V6Chain:
		f.chain = nil
	case strings.HasPrefix(a, "-w -A "+V6Chain):
		f.chain = append(f.chain, strings.TrimPrefix(a, "-w "))
	case a == "-w -C FORWARD -j "+V6Chain:
		if !f.jump {
			return "", errFake
		}
	case a == "-w -I FORWARD 1 -j "+V6Chain:
		f.jump = true
	}
	return "", nil
}

type fakeErr struct{}

func (fakeErr) Error() string { return "exit status 1" }

var errFake = fakeErr{}

type memStore struct{ m map[string][]byte }

func (s *memStore) LoadJSON(n string, v any) error {
	if b, ok := s.m[n]; ok {
		return json.Unmarshal(b, v)
	}
	return nil
}
func (s *memStore) SaveJSON(n string, v any) error {
	b, err := json.Marshal(v)
	s.m[n] = b
	return err
}

func setup(t *testing.T, apply bool) (*fakeRouter, *plane.Manager) {
	f := newFake()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b := New(srv.URL)
	b.Run = f.run
	m := plane.NewManager(b, &memStore{m: map[string][]byte{}}, plane.Config{
		Ifaces: map[plane.Mode]string{plane.ModeVless: "Wireguard1"}, Apply: apply, V6Deny: true,
	})
	return f, m
}

func TestPlanOnlyChangesNothing(t *testing.T) {
	f, m := setup(t, false)
	if err := m.SetDesired(plane.Desired{Lists: []plane.List{{Name: "x", Mode: plane.ModeVless, Domains: []string{"example.com"}}}}); err != nil {
		t.Fatal(err)
	}
	m.Reconcile(context.Background())
	st := m.Status()
	if f.posts != 0 || f.chainOK {
		t.Fatalf("plan-only mode changed the router: posts=%d chain=%v", f.posts, f.chainOK)
	}
	if len(st.Pending) != 2 || st.Pending[0].Kind != plane.OpCreateGroup || st.LastError != "" {
		t.Errorf("status = %+v", st)
	}
	if len(st.Foreign) != 2 || st.Foreign[0].Group != "domain-list0" || st.Foreign[0].Description != "AI" {
		t.Errorf("foreign = %+v", st.Foreign)
	}
}

func TestApplyImportMigrateAndCleanup(t *testing.T) {
	f, m := setup(t, true)
	ctx := context.Background()
	m.Reconcile(ctx) // learn foreign groups

	// import the user's AI list as VLESS: nothing moves while it's still in domain-list0
	if _, err := m.Import([]string{"domain-list0"}, plane.ModeVless); err != nil {
		t.Fatal(err)
	}
	m.Reconcile(ctx)
	st := m.Status()
	if len(st.Conflicts) != 2 || st.LastError != "" {
		t.Fatalf("conflicts = %+v err=%q", st.Conflicts, st.LastError)
	}
	if _, ok := f.groups["nuxk-vless"]; ok {
		t.Fatal("nuxk group created with every domain still held back")
	}

	// user takes chatgpt.com out of the old list → it moves to nuxk-vless
	f.mu.Lock()
	f.groups["domain-list0"] = []string{"openai.com"}
	f.mu.Unlock()
	m.Reconcile(ctx)
	if got := f.groups["nuxk-vless"]; len(got) != 1 || got[0] != "chatgpt.com" {
		t.Fatalf("nuxk-vless = %v", got)
	}
	routed := false
	for _, r := range f.routes {
		if r.Group == "nuxk-vless" && r.Interface == "Wireguard1" {
			routed = true
		}
	}
	if !routed || !f.jump || len(f.chain) != 2 || !strings.Contains(f.chain[0], "_NDM_OGDN_6_@nuxk-vless") {
		t.Fatalf("routes=%v jump=%v chain=%v", f.routes, f.jump, f.chain)
	}
	if st := m.Status(); st.LastError != "" || len(st.Pending) != 0 || st.AppliedAt == 0 {
		t.Errorf("after apply: %+v", st)
	}

	// idempotent: another pass posts nothing
	before := f.posts
	m.Reconcile(ctx)
	if f.posts != before {
		t.Errorf("converged pass posted %d changes", f.posts-before)
	}

	// Keenetic rebuilt its firewall: our jump is gone → put back
	f.mu.Lock()
	f.jump = false
	f.mu.Unlock()
	m.Reconcile(ctx)
	if !f.jump {
		t.Error("v6 jump not restored after a firewall rebuild")
	}

	// remove every list: route, v6 rules, then the group; user objects intact
	if err := m.SetDesired(plane.Desired{}); err != nil {
		t.Fatal(err)
	}
	m.Reconcile(ctx)
	if _, ok := f.groups["nuxk-vless"]; ok || len(f.chain) != 0 {
		t.Fatalf("cleanup left groups=%v chain=%v (err %q)", f.groups, f.chain, m.Status().LastError)
	}
	if len(f.groups["domain-list1"]) != 1 || len(f.routes) != 2 {
		t.Errorf("user objects changed: groups=%v routes=%v", f.groups, f.routes)
	}
}

func TestUnknownFirmwareRefused(t *testing.T) {
	f, m := setup(t, true)
	f.release = "6.00.A.1"
	m.SetDesired(plane.Desired{Lists: []plane.List{{Name: "x", Mode: plane.ModeVless, Domains: []string{"example.com"}}}})
	m.Reconcile(context.Background())
	if f.posts != 0 || !strings.Contains(m.Status().LastError, "6.00.A.1") {
		t.Errorf("posts=%d err=%q", f.posts, m.Status().LastError)
	}
}

func TestErrorStatusFailsTheOp(t *testing.T) {
	f := newFake()
	srv := httptest.NewServer(f)
	defer srv.Close()
	b := New(srv.URL)
	err := b.Apply(context.Background(), plane.Op{Kind: plane.OpAddRoute, Group: "nuxk-ghost", Interface: "Wireguard1"})
	if err == nil || !strings.Contains(err.Error(), "unknown object-group") {
		t.Errorf("err = %v", err)
	}
	if err := b.Apply(context.Background(), plane.Op{Kind: plane.OpDeleteGroup, Group: "domain-list0"}); err == nil {
		t.Error("backend must refuse non-nuxk objects")
	}
}

func TestMissingInterfaceSkippedWithWarning(t *testing.T) {
	f, m := setup(t, true)
	m.Cfg.Ifaces[plane.ModeWarp] = "OpkgTun0" // usque not installed on this router
	m.SetDesired(plane.Desired{Lists: []plane.List{
		{Name: "w", Mode: plane.ModeWarp, Domains: []string{"x.com"}},
		{Name: "v", Mode: plane.ModeVless, Domains: []string{"y.com"}},
	}})
	m.Reconcile(context.Background())
	st := m.Status()
	if _, ok := f.groups["nuxk-warp"]; ok || len(st.Warnings) != 1 || !strings.Contains(st.Warnings[0], "OpkgTun0") {
		t.Fatalf("warp: groups=%v warnings=%v", f.groups, st.Warnings)
	}
	if _, ok := f.groups["nuxk-vless"]; !ok || st.LastError != "" {
		t.Errorf("vless not applied: %v err=%q", f.groups, st.LastError)
	}
}

func TestDesyncListAndWarpDomainsGoToNfqws2(t *testing.T) {
	_, m := setup(t, true)
	var got [][]string
	fail := true
	m.Cfg.Desync = func(_ context.Context, d []string) error {
		got = append(got, d)
		if fail {
			fail = false
			return errors.New("shim down")
		}
		return nil
	}
	m.Cfg.Ifaces[plane.ModeWarp] = "OpkgTun0"
	m.SetDesired(plane.Desired{Lists: []plane.List{
		{Name: "dpi", Mode: plane.ModeDesync, Domains: []string{"www.youtube.com", "youtube.com", "rutracker.org"}},
	}})
	ctx := context.Background()
	m.Reconcile(ctx)
	st := m.Status()
	want := []string{"cloudflareclient.com", "rutracker.org", "youtube.com"}
	if !reflect.DeepEqual(st.Desync, want) || st.DesyncOK || len(st.Warnings) != 1 {
		t.Fatalf("first pass (push fails): %+v", st)
	}
	m.Reconcile(ctx)
	m.Reconcile(ctx) // converged: no third push
	if st := m.Status(); !st.DesyncOK || len(got) != 2 || !reflect.DeepEqual(got[1], want) {
		t.Fatalf("pushes=%v status=%+v", got, st)
	}
	// a desync list never becomes a firmware group
	for _, g := range m.Status().Groups {
		if g.Mode == plane.ModeDesync {
			t.Errorf("desync group built: %+v", g)
		}
	}
}

func TestOnDownBlockDropsAuto(t *testing.T) {
	f, m := setup(t, true)
	ctx := context.Background()
	lists := []plane.List{{Name: "v", Mode: plane.ModeVless, Domains: []string{"y.com"}}}
	m.SetDesired(plane.Desired{Lists: lists})
	m.Reconcile(ctx)
	auto := func() (bool, int) {
		n, a := 0, false
		for _, r := range f.routes {
			if r.Group == "nuxk-vless" {
				n++
				a = r.Auto
			}
		}
		return a, n
	}
	if a, n := auto(); !a || n != 1 {
		t.Fatalf("direct: auto=%v routes=%d", a, n)
	}
	if err := m.SetDesired(plane.Desired{Lists: lists, OnDown: "sometimes"}); err == nil {
		t.Error("bad on_down accepted")
	}
	m.SetDesired(plane.Desired{Lists: lists, OnDown: plane.OnDownBlock})
	m.Reconcile(ctx)
	if a, n := auto(); a || n != 1 || m.Status().LastError != "" {
		t.Fatalf("block: auto=%v routes=%d err=%q", a, n, m.Status().LastError)
	}
	before := f.posts
	m.Reconcile(ctx)
	if f.posts != before {
		t.Error("block policy not converged")
	}
}

// Without any «DPI» list the user's own nfqws2 hostlist is never touched —
// not even to add the WARP hosts. Once nuxk owns it, removing the last DPI
// list clears it (instead of leaving stale domains behind).
func TestDesyncListUntouchedUntilADPIListExists(t *testing.T) {
	_, m := setup(t, true)
	var pushes [][]string
	m.Cfg.Desync = func(_ context.Context, d []string) error { pushes = append(pushes, d); return nil }
	m.Cfg.Ifaces[plane.ModeWarp] = "OpkgTun0"
	ctx := context.Background()
	warp := []plane.List{{Name: "w", Mode: plane.ModeWarp, Domains: []string{"x.com"}}}
	m.SetDesired(plane.Desired{Lists: warp})
	m.Reconcile(ctx)
	if len(pushes) != 0 || m.Status().DesyncOn {
		t.Fatalf("pushed without a DPI list: %v", pushes)
	}
	m.SetDesired(plane.Desired{Lists: append(warp, plane.List{Name: "d", Mode: plane.ModeDesync, Domains: []string{"y.com"}})})
	m.Reconcile(ctx)
	if len(pushes) != 1 || !reflect.DeepEqual(pushes[0], []string{"cloudflareclient.com", "y.com"}) {
		t.Fatalf("pushes = %v", pushes)
	}
	m.SetDesired(plane.Desired{Lists: warp}) // last DPI list removed
	m.Reconcile(ctx)
	if len(pushes) != 2 || !reflect.DeepEqual(pushes[1], []string{"cloudflareclient.com"}) {
		t.Fatalf("after removing DPI: %v", pushes)
	}
}

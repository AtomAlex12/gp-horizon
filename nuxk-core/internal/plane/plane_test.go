package plane

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	got := Normalize([]string{" OpenAI.com ", "api.openai.com", "*.claude.ai", "claude.ai.", "x.y.claude.ai",
		"# comment", "", "localhost", "bad_name.com", "a..b.com", "chatgpt.com", "chatgpt.com"})
	want := []string{"chatgpt.com", "claude.ai", "openai.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Normalize = %v, want %v", got, want)
	}
}

func TestBuildOneGroupPerRoutedMode(t *testing.T) {
	d := Desired{Lists: []List{
		{Name: "AI", Mode: ModeVless, Domains: []string{"openai.com", "claude.ai"}},
		{Name: "Meta", Mode: ModeWarp, Domains: []string{"instagram.com"}},
		{Name: "More AI", Mode: ModeVless, Domains: []string{"api.openai.com", "grok.com"}},
	}}
	g := Build(d, map[Mode]string{ModeVless: "Wireguard1"}) // no warp tunnel yet
	if len(g) != 1 || g[0].Name != "nuxk-vless" || g[0].Interface != "Wireguard1" {
		t.Fatalf("groups = %+v", g)
	}
	if !reflect.DeepEqual(g[0].Domains, []string{"claude.ai", "grok.com", "openai.com"}) {
		t.Errorf("domains = %v", g[0].Domains)
	}
}

func kinds(ops []Op) []string {
	var out []string
	for _, o := range ops {
		out = append(out, string(o.Kind)+":"+o.Group)
	}
	return out
}

func TestPlanFromScratch(t *testing.T) {
	want := []Group{{Name: "nuxk-vless", Mode: ModeVless, Interface: "Wireguard1", Domains: []string{"a.com", "b.com"}}}
	obs := Observed{Groups: map[string][]string{}}
	ops, conf := Plan(want, obs, true)
	if got := kinds(ops); !reflect.DeepEqual(got, []string{"create-group:nuxk-vless", "add-route:nuxk-vless", "ensure-v6-deny:"}) {
		t.Errorf("ops = %v", got)
	}
	if len(conf) != 0 || !reflect.DeepEqual(ops[2].Groups, []string{"nuxk-vless"}) {
		t.Errorf("conf=%v v6=%v", conf, ops[2].Groups)
	}
}

func TestPlanNeverTouchesUserObjects(t *testing.T) {
	obs := Observed{
		Groups: map[string][]string{"domain-list0": {"openai.com"}, "nuxk-old": {"x.com"}},
		Routes: []Route{{Group: "domain-list0", Interface: "Wireguard1"}, {Group: "nuxk-old", Interface: "Wireguard1"}},
	}
	ops, _ := Plan(nil, obs, true)
	for _, o := range ops {
		if o.Group != "" && !Owned(o.Group) {
			t.Fatalf("plan touches user object: %+v", o)
		}
	}
	// stale nuxk group: v6 rules cleared first, then route, then group
	if got := kinds(ops); !reflect.DeepEqual(got, []string{"ensure-v6-deny:", "del-route:nuxk-old", "delete-group:nuxk-old"}) {
		t.Errorf("ops = %v", got)
	}
}

func TestPlanHoldsBackDomainsStillInUserLists(t *testing.T) {
	obs := Observed{
		Groups: map[string][]string{"domain-list0": {"openai.com", "api.stripe.com"}, "unrouted": {"b.com"}},
		Routes: []Route{{Group: "domain-list0", Interface: "Wireguard1"}},
	}
	want := []Group{{Name: "nuxk-vless", Interface: "Wireguard1",
		Domains: []string{"api.openai.com", "b.com", "chatgpt.com", "openai.com", "stripe.com"}}}
	ops, conf := Plan(want, obs, false)
	if len(ops) == 0 || ops[0].Kind != OpCreateGroup || !reflect.DeepEqual(ops[0].Domains, []string{"b.com", "chatgpt.com"}) {
		t.Fatalf("create = %+v", ops)
	}
	held := map[string]string{}
	for _, c := range conf {
		held[c.Domain] = c.UserGroup
	}
	// same name, a parent in the user list, and a user subdomain under ours
	for _, d := range []string{"openai.com", "api.openai.com", "stripe.com"} {
		if held[d] != "domain-list0" {
			t.Errorf("%s not held back: %v", d, held)
		}
	}
	if _, ok := held["b.com"]; ok {
		t.Error("an unrouted user group is not a conflict")
	}
}

func TestPlanUpdatesInPlace(t *testing.T) {
	obs := Observed{
		Groups: map[string][]string{"nuxk-vless": {"a.com", "old.com"}},
		Routes: []Route{{Group: "nuxk-vless", Interface: "Wireguard0"}},
	}
	want := []Group{{Name: "nuxk-vless", Interface: "Wireguard1", Domains: []string{"a.com", "new.com"}}}
	ops, _ := Plan(want, obs, true)
	if got := kinds(ops); !reflect.DeepEqual(got, []string{"add-domains:nuxk-vless", "add-route:nuxk-vless", "ensure-v6-deny:", "del-domains:nuxk-vless", "del-route:nuxk-vless"}) {
		t.Fatalf("ops = %v", got)
	}
	if ops[3].Domains[0] != "old.com" || ops[4].Interface != "Wireguard0" || ops[1].Interface != "Wireguard1" {
		t.Errorf("ops = %+v", ops)
	}
	// converged: nothing but the idempotent v6 check
	obs2 := Observed{Groups: map[string][]string{"nuxk-vless": {"a.com", "new.com"}}, Routes: []Route{{Group: "nuxk-vless", Interface: "Wireguard1"}}}
	ops2, _ := Plan(want, obs2, true)
	if Changes(ops2) != 0 {
		t.Errorf("converged plan = %v", kinds(ops2))
	}
}

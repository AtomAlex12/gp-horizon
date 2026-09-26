package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Strategy is one nfqws2 profile nuxk puts in front of the stock ones: this
// desync, for these domains only (a hostlist of its own). It comes from a
// strategy search (GP) and is applied by the person, never on its own.
type Strategy struct {
	ID        string   `json:"id"`
	Protocol  string   `json:"protocol"` // tls | http | quic
	Domains   []string `json:"domains"`
	Args      string   `json:"args"`             // nfqws2 desync args, see ValidateStrategy
	Source    string   `json:"source,omitempty"` // where it was found, e.g. "gp:<run_id>"
	AppliedAt int64    `json:"applied_at,omitempty"`
}

// Strategist is implemented by engines that take per-domain strategies
// (nfqws2). Like Configurable, callers type-assert for it.
type Strategist interface {
	ApplyStrategies(ctx context.Context, s []Strategy) error
}

// Filters per protocol: the profile only ever sees its own traffic kind.
var strategyFilter = map[string]string{
	"tls":  "--filter-tcp=443 --filter-l7=tls",
	"http": "--filter-tcp=80 --filter-l7=http",
	"quic": "--filter-udp=443 --filter-l7=quic",
}

var (
	// the only nfqws2 options a strategy may carry; values without files
	// (@…), paths, quotes, shell or glob characters — the stock init script
	// expands the config unquoted
	argRe    = regexp.MustCompile(`^--(payload|lua-desync|out-range|in-range)=[A-Za-z0-9_.,:=+<%-]+$`)
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)
	idRe     = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,80}$`)
)

// ValidDomain: a lower-case host name, nothing a shell or a hostlist could
// read as anything else.
func ValidDomain(d string) bool { return domainRe.MatchString(d) }

// NormalizeArgs drops what a strategy search prints around the desync
// itself — the program name and its own filters/hostlists (nuxk sets both) —
// and returns the args one space apart.
func NormalizeArgs(args string) string {
	var out []string
	for i, t := range strings.Fields(args) {
		if i == 0 && (t == "nfqws2" || strings.HasSuffix(t, "/nfqws2")) {
			continue
		}
		if strings.HasPrefix(t, "--filter-") || strings.HasPrefix(t, "--hostlist") {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, " ")
}

// ValidateStrategy: what may reach the router's nfqws2.conf.
func ValidateStrategy(s Strategy) error {
	if !idRe.MatchString(s.ID) {
		return fmt.Errorf("strategy id %q", s.ID)
	}
	if strategyFilter[s.Protocol] == "" {
		return fmt.Errorf("protocol %q: want tls, http or quic", s.Protocol)
	}
	if len(s.Domains) == 0 || len(s.Domains) > 5000 {
		return fmt.Errorf("a strategy needs 1..5000 domains, got %d", len(s.Domains))
	}
	for _, d := range s.Domains {
		if !domainRe.MatchString(d) {
			return fmt.Errorf("not a domain: %q", d)
		}
	}
	toks := strings.Fields(s.Args)
	if len(toks) == 0 || len(s.Args) > 2000 {
		return fmt.Errorf("args: 1..2000 characters")
	}
	desync := false
	for _, t := range toks {
		if !argRe.MatchString(t) {
			return fmt.Errorf("argument %q is not allowed (only --payload, --lua-desync, --out-range, --in-range, without files)", t)
		}
		desync = desync || strings.HasPrefix(t, "--lua-desync=")
	}
	if !desync {
		return fmt.Errorf("args have no --lua-desync")
	}
	return nil
}

// RenderStrategies is the NFQWS_ARGS_CUSTOM value: one profile per strategy,
// each with its protocol filter and its hostlist listDir/nuxk-sN.list.
func RenderStrategies(ss []Strategy, listDir string) string {
	parts := make([]string, 0, len(ss))
	for i, s := range ss {
		parts = append(parts, fmt.Sprintf("%s --hostlist=%s/nuxk-s%d.list %s", strategyFilter[s.Protocol], listDir, i+1, strings.Join(strings.Fields(s.Args), " ")))
	}
	return strings.Join(parts, " --new ")
}

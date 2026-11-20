// Package entware routes the logical WARP group to the Linux AWG device.
package entware

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"nuxk.dev/horizon/core/internal/dns"
	"nuxk.dev/horizon/core/internal/engine"
	"nuxk.dev/horizon/core/internal/plane"
)

const (
	set4 = "NUXK_WARP_AWG4"
)

type scopedRCI interface {
	ScopedNameServer(context.Context, string, string, bool) error
}

type Routing struct {
	Addr      string
	RouterDNS string
	Script    engine.Exec
	RCI       scopedRCI
	Store     plane.Store

	mu         sync.RWMutex
	names      []string
	cancel     func()
	block      bool
	verifyName string
	verified   bool
	bound      bool
}

const registration = "warp-awg-dns"

type journal struct {
	Active bool     `json:"active"`
	Addr   string   `json:"addr,omitempty"`
	Names  []string `json:"names"`
}

func (r *Routing) Check(ctx context.Context) error {
	host, port, err := net.SplitHostPort(r.Addr)
	ip, ipErr := netip.ParseAddr(host)
	number, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || ipErr != nil || !ip.Is4() || !ip.IsPrivate() || portErr != nil || number == 0 || number == 53 {
		return errors.New("DNS WARP: укажите WARP_AWG_DNS_ADDR как локальный IPv4-адрес роутера и отдельный порт")
	}
	return r.Script.Action(ctx, "route-check")
}

// Set reconciles only the AWG group's registrations. The union of old and
// new registrations is recorded before touching RCI so that a restart can
// retry cleanup even after a partially applied change.
func (r *Routing) Set(ctx context.Context, names []string, block bool) error {
	var state journal
	if err := r.Store.LoadJSON(registration, &state); err != nil {
		return err
	}
	names = plane.Normalize(names)
	if !state.Active && len(names) == 0 {
		return nil
	}
	if state.Active && state.Addr != r.Addr {
		return errors.New("DNS WARP: верните прежний WARP_AWG_DNS_ADDR и переключитесь на USQUE перед сменой адреса")
	}
	previous := state.Names
	// Do not fold subdomains in the journal: each registration must be
	// removed individually even when the new list includes its parent.
	union := slices.Clone(previous)
	for _, name := range names {
		if !slices.Contains(union, name) {
			union = append(union, name)
		}
	}
	if !state.Active || !slices.Equal(previous, union) {
		if err := r.Store.SaveJSON(registration, journal{Active: true, Addr: r.Addr, Names: union}); err != nil {
			return err
		}
	}
	r.mu.RLock()
	checkDNS := !r.bound || !slices.Equal(r.names, names)
	r.mu.RUnlock()
	if len(names) > 0 {
		if err := r.enable(ctx, names, block); err != nil {
			return err
		}
	}
	for _, name := range names {
		if err := r.RCI.ScopedNameServer(ctx, r.Addr, name, true); err != nil {
			return err
		}
	}
	for _, name := range previous {
		if !slices.Contains(names, name) {
			if err := r.RCI.ScopedNameServer(ctx, r.Addr, name, false); err != nil {
				return err
			}
		}
	}
	if len(names) > 0 && checkDNS {
		if err := r.verify(ctx, names[0]); err != nil {
			r.mu.Lock()
			r.bound = false
			r.mu.Unlock()
			return err
		}
		r.mu.Lock()
		r.bound = true
		r.mu.Unlock()
	}
	if len(names) == 0 {
		if err := r.disable(ctx); err != nil {
			return err
		}
		return r.Store.SaveJSON(registration, journal{})
	}
	if !slices.Equal(union, names) {
		return r.Store.SaveJSON(registration, journal{Active: true, Addr: r.Addr, Names: names})
	}
	return nil
}

func (r *Routing) enable(ctx context.Context, names []string, block bool) error {
	if err := r.Check(ctx); err != nil {
		return err
	}
	policy := "direct"
	if block {
		policy = "block"
	}
	if _, err := r.Script.ActionWithInput(ctx, "route-enable", policy+"\n"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names, r.block = names, block
	if r.cancel == nil {
		closeDNS, err := dns.StartScoped(r.Addr, r.paths, r.covers, record)
		if err != nil {
			return err
		}
		r.cancel = closeDNS
	}
	return nil
}

func (r *Routing) disable(ctx context.Context) error {
	if err := r.Script.Action(ctx, "route-disable"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.names = nil
	r.bound = false
	return nil
}

func (r *Routing) paths() []dns.Path {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.block {
		if _, err := net.InterfaceByName("awg0"); err != nil {
			return []dns.Path{{Name: dns.PathDirect}}
		}
	}
	return []dns.Path{{Name: dns.ViaWarp, Iface: "awg0"}}
}

func (r *Routing) covers(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == r.verifyName && name != "" {
		r.verified = true
	}
	for _, domain := range r.names {
		if name == domain || strings.HasSuffix(name, "."+domain) {
			return true
		}
	}
	return false
}

// Verify that a fresh WARP query actually traverses the scoped listener.
// A successful RCI response alone does not confirm how Keenetic selected DNS.
func (r *Routing) verify(ctx context.Context, domain string) error {
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := fmt.Sprintf("nuxk-%x.%s", nonce, domain)
	query, err := dns.Query(1, name, dns.TypeA)
	if err != nil {
		return errors.New("DNS WARP: не удалось создать проверочный запрос")
	}
	r.mu.Lock()
	r.verifyName, r.verified = name, false
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.verifyName, r.verified = "", false
		r.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "udp4", r.RouterDNS)
	if err != nil {
		return fmt.Errorf("DNS WARP: DNS Keenetic недоступен: %w", err)
	}
	defer c.Close()
	deadline, _ := ctx.Deadline()
	_ = c.SetDeadline(deadline)
	if _, err := c.Write(query); err != nil {
		return err
	}
	var response [4096]byte
	n, err := c.Read(response[:])
	if err != nil {
		return fmt.Errorf("DNS WARP: Keenetic не ответил на проверочный запрос: %w", err)
	}
	parsed, err := dns.Parse(response[:n])
	r.mu.RLock()
	verified := r.verified
	r.mu.RUnlock()
	if err != nil || !verified || (parsed.Rcode != dns.RcodeNoError && parsed.Rcode != dns.RcodeNXDomain) {
		return errors.New("DNS WARP: Keenetic не использовал выделенный DNS или обработчик не получил ответ")
	}
	return nil
}

func record(ctx context.Context, addrs []netip.Addr, ttl uint32) error {
	var batch strings.Builder
	timeout := max(1, min(ttl, 2147483)) // ipset's timeout limit is about 24 days
	for _, addr := range addrs {
		fmt.Fprintf(&batch, "add %s %s timeout %d -exist\n", set4, addr.String(), timeout)
	}
	cmd := exec.CommandContext(ctx, "ipset", "restore")
	cmd.Stdin = strings.NewReader(batch.String())
	return cmd.Run()
}

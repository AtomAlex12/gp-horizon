package suggest

import (
	"context"
	"log/slog"
	"strings"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
)

// After acceptance the accepted way is measured again: once the plane has
// had time to apply it, then daily. DPI is where this matters most — it can't
// be tried before the domain is in nfqws2's list. Only measuring: a failed
// check advises (a strategy search, a way to use meanwhile) and changes
// nothing.

// verifyDue verifies the one accepted item most overdue, if any.
func (s *Service) verifyDue(ctx context.Context) {
	now := s.now().Unix()
	s.mu.Lock()
	var pick *Item
	for _, it := range s.items {
		if it.State == StateAccepted && it.VerifyDue != 0 && it.VerifyDue <= now && (pick == nil || it.VerifyDue < pick.VerifyDue) {
			pick = it
		}
	}
	var it Item
	if pick != nil {
		it = *pick
	}
	s.mu.Unlock()
	if pick != nil {
		s.verify(ctx, it)
	}
}

func (s *Service) verify(ctx context.Context, it Item) {
	now := s.now()
	update := func(f func(*Item)) {
		s.mu.Lock()
		defer s.mu.Unlock()
		cur := s.items[it.Domain]
		if cur == nil || cur.State != StateAccepted || cur.AcceptedAt != it.AcceptedAt {
			return // dismissed, re-checked or accepted anew meanwhile
		}
		f(cur)
		s.saveLocked()
	}
	if note, stop := s.applied(it); note != "" {
		update(func(c *Item) {
			c.VerifyNote, c.VerifyDue = note, now.Add(verifyWait).Unix()
			if stop {
				c.VerifyDue = 0
			}
		})
		return
	}
	via, ok := modeVia[it.Accepted]
	if !ok {
		update(func(c *Item) { c.VerifyDue = 0 })
		return
	}

	pctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()
	b := s.bar(pctx)
	var scratch Item
	l := &ladder{p: s.P, bar: b, desync: s.desync(), domain: it.Domain, it: &scratch, seen: map[probe.Via]judged{}}
	ip, why := s.P.Resolve(pctx, it.Domain)
	if why != "" {
		update(func(c *Item) {
			c.Verified, c.VerifiedAt, c.VerifyDue = false, now.Unix(), now.Add(reverifyEvery).Unix()
			c.VerifyNote = "Проверить не удалось: " + map[string]string{
				"bogus": "DNS вернул подменённый ответ", "nxdomain": "DNS не знает такого имени",
			}[why] + "."
			if why == "timeout" {
				c.VerifyNote = "Проверить не удалось: DNS не ответил вовремя."
			}
		})
		return
	}
	l.ip = ip
	j := l.measure(pctx, via)
	var next, interim, note string
	switch {
	case j.ok && via == probe.ViaDesync:
		note = "Проверено после применения: с DPI (nfqws2) сайт открывается."
	case j.ok:
		note = "Проверено после применения: через " + title[via] + " сайт открывается."
	case via == probe.ViaDesync:
		next = NextSearch
		note = "С DPI (nfqws2) сайт так и не открывается как надо (" + j.why + "): известные стратегии не помогают. " +
			"Нужен подбор стратегии — GP на Raspberry Pi."
		if ok, _ := l.tunnels(pctx); ok != "" {
			interim = string(ok)
			note += " Пока он идёт, сайт можно пустить через " + title[ok] + " — через него открывается (проверено)."
		} else {
			note += " Туннели сейчас тоже не выручают: " + l.summary(probe.ViaWarp, probe.ViaVless) + "."
		}
	default:
		note = "После применения через " + title[via] + " сайт не открывается как надо (" + j.why + "). Проверьте, работает ли туннель (раздел «Движки»)."
	}
	update(func(c *Item) {
		c.Verified, c.VerifiedAt, c.VerifyDue = j.ok, now.Unix(), now.Add(reverifyEvery).Unix()
		c.Next, c.Interim, c.VerifyNote = next, interim, note
		c.Paths = mergePaths(c.Paths, scratch.Paths)
	})
	slog.Info("suggest: verified", "domain", it.Domain, "mode", it.Accepted, "ok", j.ok, "next", next, "interim", interim)
}

// applied: "" when the plane carries the domain for the accepted mode;
// otherwise what the check waits for (stop: it won't come — the domain was
// taken out of the lists).
func (s *Service) applied(it Item) (note string, stop bool) {
	if s.Router == nil {
		return "Проверка ждёт: маршрутизация выключена (PLANE в nuxk.conf).", false
	}
	if s.Router.Covered(it.Domain) == "" {
		return "Домен убран из списков — проверка остановлена.", true
	}
	st := s.Router.Status()
	if !st.Apply {
		return "Проверка — после применения: маршрутизация сейчас в режиме плана (PLANE_APPLY=0), роутер не меняется.", false
	}
	if it.Accepted == string(plane.ModeDesync) {
		if !st.DesyncOn || !st.DesyncOK || !covers(st.Desync, it.Domain) {
			return "Проверка ждёт, пока список DPI дойдёт до nfqws2.", false
		}
		return "", false
	}
	for _, g := range st.Groups {
		if string(g.Mode) == it.Accepted && covers(g.Domains, it.Domain) && len(st.Pending) == 0 {
			return "", false
		}
	}
	return "Проверка ждёт, пока список применится на роутере.", false
}

// covers: some entry equals domain or is a parent of it (the firmware and
// nfqws2 cover subdomains).
func covers(list []string, domain string) bool {
	for _, e := range list {
		e = strings.TrimPrefix(strings.ToLower(e), "*.")
		if e == domain || strings.HasSuffix(domain, "."+e) {
			return true
		}
	}
	return false
}

// mergePaths: fresh measurements replace the same way's old ones.
func mergePaths(old, fresh []Path) []Path {
	out := append([]Path{}, old...)
	for _, f := range fresh {
		replaced := false
		for i := range out {
			if out[i].Via == f.Via {
				out[i], replaced = f, true
			}
		}
		if !replaced {
			out = append(out, f)
		}
	}
	return out
}

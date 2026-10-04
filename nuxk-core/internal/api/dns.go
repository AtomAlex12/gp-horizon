package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"nuxk.dev/horizon/core/internal/dns"
)

func (d Deps) dnsOff(w http.ResponseWriter) bool {
	if d.DNS == nil {
		writeErr(w, http.StatusNotFound, "dns_off", "protected DNS is off")
		return true
	}
	return false
}

// handleDNS: whether the router's DNS proxy asks nuxk, which way the
// questions go out, how each way has been doing.
func (d Deps) handleDNS(w http.ResponseWriter, r *http.Request) {
	if d.dnsOff(w) {
		return
	}
	writeJSON(w, http.StatusOK, d.DNS.Status())
}

// handleDNSSettings turns protected DNS on or off and picks the way out.
// Turning on changes the router's DNS settings (one more server for its DNS
// proxy) — checked on the way, taken back if the router stops answering.
func (d Deps) handleDNSSettings(w http.ResponseWriter, r *http.Request) {
	if d.dnsOff(w) {
		return
	}
	// what the body leaves out stays as it is: {"via":"warp"} doesn't turn protection off
	s := d.DNS.Settings()
	s.Resolvers = nil
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&s); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"enabled":true,"via":"auto","resolvers":["cloudflare"],"cache":true,"engine":"nuxk"}`)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	st, err := d.DNS.SetSettings(ctx, s)
	switch {
	case errors.Is(err, dns.ErrBadSettings):
		writeErr(w, http.StatusBadRequest, "bad_settings", err.Error())
	case errors.Is(err, dns.ErrNoHook), errors.Is(err, dns.ErrNoSmartDNS):
		writeErr(w, http.StatusConflict, "dns_unavailable", err.Error())
	case err != nil:
		writeErr(w, http.StatusBadGateway, "dns_failed", err.Error())
	default:
		writeJSON(w, http.StatusOK, st)
	}
}

// handleDNSCheck compares, for the canaries and the person's list domains,
// the router's answer and a plain resolver's with DoH through the tunnel.
func (d Deps) handleDNSCheck(w http.ResponseWriter, r *http.Request) {
	if d.dnsOff(w) {
		return
	}
	var in struct {
		Domains []string `json:"domains"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil && err != io.EOF {
			writeErr(w, http.StatusBadRequest, "bad_body", `want {"domains":["example.com"]} or nothing`)
			return
		}
	}
	if len(in.Domains) > 20 {
		in.Domains = in.Domains[:20]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, d.DNS.Check(ctx, in.Domains))
}

// handleDNSCacheFlush forgets the kept answers: the next questions go to the
// resolvers.
func (d Deps) handleDNSCacheFlush(w http.ResponseWriter, r *http.Request) {
	if d.dnsOff(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	st, err := d.DNS.FlushCache(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "dns_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleDNSStats: the last hour of answers, minute by minute — what came
// from the cache, from a server, expired, or not at all.
func (d Deps) handleDNSStats(w http.ResponseWriter, r *http.Request) {
	if d.dnsOff(w) {
		return
	}
	writeJSON(w, http.StatusOK, d.DNS.Stats())
}

// handleDNSLog: the last questions and their answers, newest first;
// ?q= filters by part of the name, ?n= how many (up to 200).
func (d Deps) handleDNSLog(w http.ResponseWriter, r *http.Request) {
	if d.dnsOff(w) {
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	writeJSON(w, http.StatusOK, map[string]any{"items": d.DNS.Log(r.URL.Query().Get("q"), n)})
}

package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"time"
)

// RouteAnswer is called before returning a WARP DNS answer to the firmware.
// It records the destinations in the Entware routing set.
type RouteAnswer func(context.Context, []netip.Addr, uint32) error

// StartScoped listens separately from the optional system-wide protected DNS.
// The caller owns the per-domain registration with the firmware. Only local
// requests for accepted names receive answers.
func StartScoped(addr string, paths func() []Path, covers func(string) bool, route RouteAnswer) (func(), error) {
	s := New(Options{Listen: addr, Paths: paths}, nil)
	s.scoped = func(q []byte) []byte {
		if binary.BigEndian.Uint16(q[4:6]) != 1 || !covers(QName(q)) {
			return ServFail(q)
		}
		switch QType(q) {
		case TypeA, 5, 16, 15:
			// Address, alias, TXT and MX. IPv6 and HTTPS/SVCB address
			// hints are omitted while this transport is IPv4 only.
		default:
			resp := ServFail(q)
			if resp != nil {
				resp[3] = 0x80 // NOERROR/NODATA
			}
			return resp
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		resp, _, err := s.resolve(ctx, q)
		if err != nil {
			return ServFail(q)
		}
		answer, err := Parse(resp)
		if err != nil {
			return ServFail(q)
		}
		var ipv4 []netip.Addr
		for _, ip := range answer.Addrs {
			if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
				return ServFail(q)
			}
			ipv4 = append(ipv4, ip)
		}
		if len(ipv4) > 0 {
			if err := route(ctx, ipv4, answer.AddrTTL); err != nil {
				return ServFail(q)
			}
		}
		return resp
	}
	if covers == nil || route == nil {
		return nil, errors.New("DNS WARP: отсутствует обработчик маршрутизации")
	}
	if err := s.start(); err != nil {
		return nil, err
	}
	return s.stop, nil
}

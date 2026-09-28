package dns

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
)

// Just enough of the DNS wire format (RFC 1035) for a forwarder that passes
// messages through untouched: a query's size limit (EDNS), a truncated
// answer for UDP, and — for the spoofing check — a query and the addresses
// in its answer.

const (
	TypeA    = 1
	TypeAAAA = 28
	typeOPT  = 41

	RcodeNoError  = 0
	RcodeServFail = 2
	RcodeNXDomain = 3

	headerLen = 12
)

var errShort = errors.New("dns: short message")

// skipName returns the offset just past the (possibly compressed) name at off.
func skipName(m []byte, off int) (int, error) {
	for i := 0; i < 128; i++ { // labels of a legal name, pointers included
		if off >= len(m) {
			return 0, errShort
		}
		l := int(m[off])
		switch {
		case l == 0:
			return off + 1, nil
		case l&0xC0 == 0xC0: // a pointer ends the name here
			if off+2 > len(m) {
				return 0, errShort
			}
			return off + 2, nil
		case l&0xC0 != 0:
			return 0, errors.New("dns: bad label")
		default:
			off += 1 + l
		}
	}
	return 0, errors.New("dns: name too long")
}

// questionEnd: the offset just past the question section.
func questionEnd(m []byte) (int, error) {
	if len(m) < headerLen {
		return 0, errShort
	}
	off := headerLen
	for n := int(binary.BigEndian.Uint16(m[4:6])); n > 0; n-- {
		var err error
		if off, err = skipName(m, off); err != nil {
			return 0, err
		}
		off += 4 // type, class
		if off > len(m) {
			return 0, errShort
		}
	}
	return off, nil
}

// UDPLimit: how big an answer the asker takes over UDP — 512, or what its
// EDNS OPT record says (capped at 1232, the size that avoids fragmentation).
func UDPLimit(q []byte) int {
	limit := 512
	off, err := questionEnd(q)
	if err != nil {
		return limit
	}
	rrs := int(binary.BigEndian.Uint16(q[6:8])) + int(binary.BigEndian.Uint16(q[8:10])) + int(binary.BigEndian.Uint16(q[10:12]))
	for ; rrs > 0; rrs-- {
		if off, err = skipName(q, off); err != nil || off+10 > len(q) {
			return limit
		}
		typ := binary.BigEndian.Uint16(q[off : off+2])
		class := int(binary.BigEndian.Uint16(q[off+2 : off+4]))
		rdlen := int(binary.BigEndian.Uint16(q[off+8 : off+10]))
		if typ == typeOPT && class > limit {
			limit = min(class, 1232)
		}
		off += 10 + rdlen
	}
	return limit
}

// Truncated: resp cut to its header and question with TC set — the asker
// retries over TCP.
func Truncated(resp []byte) []byte {
	end, err := questionEnd(resp)
	if err != nil {
		end = headerLen
	}
	if end > len(resp) {
		end = len(resp)
	}
	out := append([]byte{}, resp[:end]...)
	if len(out) < headerLen {
		return out
	}
	out[2] |= 0x02 // TC
	for i := 6; i < 12; i++ {
		out[i] = 0 // no answer, authority, additional records
	}
	if err != nil {
		out[4], out[5] = 0, 0
	}
	return out
}

// ServFail: an error answer to q (its ID and question), for when every way
// to the resolvers failed.
func ServFail(q []byte) []byte {
	end, err := questionEnd(q)
	if err != nil || len(q) < headerLen {
		return nil
	}
	out := append([]byte{}, q[:end]...)
	out[2] = 0x80 | (q[2] & 0x79) // QR, the query's opcode and RD
	out[3] = 0x80 | RcodeServFail // RA
	for i := 6; i < 12; i++ {
		out[i] = 0
	}
	return out
}

// QName: the first question's name, lower case, without the trailing dot.
func QName(m []byte) string {
	if len(m) < headerLen || binary.BigEndian.Uint16(m[4:6]) == 0 {
		return ""
	}
	var b strings.Builder
	off := headerLen
	for off < len(m) {
		l := int(m[off])
		if l == 0 || l&0xC0 != 0 || off+1+l > len(m) {
			break
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strings.ToLower(string(m[off+1 : off+1+l])))
		off += 1 + l
	}
	return b.String()
}

// Query builds a recursive query for name with the given ID.
func Query(id uint16, name string, qtype uint16) ([]byte, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" || len(name) > 253 {
		return nil, errors.New("dns: bad name")
	}
	m := make([]byte, headerLen, headerLen+len(name)+6)
	binary.BigEndian.PutUint16(m[0:2], id)
	m[2] = 0x01 // RD
	binary.BigEndian.PutUint16(m[4:6], 1)
	for _, l := range strings.Split(name, ".") {
		if l == "" || len(l) > 63 {
			return nil, errors.New("dns: bad label in " + name)
		}
		m = append(m, byte(len(l)))
		m = append(m, l...)
	}
	m = append(m, 0)
	m = binary.BigEndian.AppendUint16(m, qtype)
	m = binary.BigEndian.AppendUint16(m, 1) // IN
	return m, nil
}

// Answer is what the check needs from a response.
type Answer struct {
	Rcode int
	Addrs []netip.Addr // A and AAAA records, in order
}

// Parse reads the rcode and the addresses of a response.
func Parse(resp []byte) (Answer, error) {
	var a Answer
	off, err := questionEnd(resp)
	if err != nil {
		return a, err
	}
	if resp[2]&0x80 == 0 {
		return a, errors.New("dns: not a response")
	}
	a.Rcode = int(resp[3] & 0x0F)
	for n := int(binary.BigEndian.Uint16(resp[6:8])); n > 0; n-- {
		if off, err = skipName(resp, off); err != nil {
			return a, err
		}
		if off+10 > len(resp) {
			return a, errShort
		}
		typ := binary.BigEndian.Uint16(resp[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(resp[off+8 : off+10]))
		off += 10
		if off+rdlen > len(resp) {
			return a, errShort
		}
		rd := resp[off : off+rdlen]
		switch {
		case typ == TypeA && rdlen == 4:
			a.Addrs = append(a.Addrs, netip.AddrFrom4([4]byte(rd)))
		case typ == TypeAAAA && rdlen == 16:
			a.Addrs = append(a.Addrs, netip.AddrFrom16([16]byte(rd)))
		}
		off += rdlen
	}
	return a, nil
}

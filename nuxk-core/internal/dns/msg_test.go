package dns

import (
	"encoding/binary"
	"testing"
)

func TestQueryParse(t *testing.T) {
	q, err := Query(7, "Example.COM.", TypeA)
	if err != nil {
		t.Fatal(err)
	}
	if QName(q) != "example.com" || binary.BigEndian.Uint16(q[:2]) != 7 || q[2]&0x01 == 0 {
		t.Fatalf("query % x", q)
	}
	a, err := Parse(respond(q, "192.0.2.1", "192.0.2.2"))
	if err != nil || a.Rcode != 0 || len(a.Addrs) != 2 || a.Addrs[1].String() != "192.0.2.2" {
		t.Fatalf("%v %+v", err, a)
	}
	if _, err := Parse(q); err == nil {
		t.Fatal("a query parsed as an answer")
	}
	for _, bad := range []string{"", "a..b", string(make([]byte, 300))} {
		if _, err := Query(1, bad, TypeA); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := Parse(respond(q, "192.0.2.1")[:20]); err == nil {
		t.Fatal("cut answer parsed")
	}
}

func TestUDPLimitAndTruncation(t *testing.T) {
	q, _ := Query(1, "example.com", TypeA)
	if UDPLimit(q) != 512 {
		t.Fatalf("plain: %d", UDPLimit(q))
	}
	// EDNS OPT: root name, type 41, class = the asker's buffer
	opt := append([]byte{}, q...)
	binary.BigEndian.PutUint16(opt[10:12], 1)
	opt = append(opt, 0, 0, 41, 0x10, 0x00, 0, 0, 0, 0, 0, 0) // 4096
	if UDPLimit(opt) != 1232 {
		t.Fatalf("EDNS 4096 capped: %d", UDPLimit(opt))
	}
	resp := respond(q, bigZone()...)
	tr := Truncated(resp)
	if tr[2]&0x02 == 0 || binary.BigEndian.Uint16(tr[6:8]) != 0 || QName(tr) != "example.com" || tr[0] != resp[0] {
		t.Fatalf("truncated % x", tr)
	}
	sf := ServFail(q)
	if sf[2]&0x80 == 0 || sf[3]&0x0F != RcodeServFail || QName(sf) != "example.com" {
		t.Fatalf("servfail % x", sf)
	}
}

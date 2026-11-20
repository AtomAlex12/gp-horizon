package entware

import (
	"context"
	"net"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/dns"
)

func TestVerifyWaitsForScopedDNSAfterRouterReply(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	r := Routing{RouterDNS: conn.LocalAddr().String(), names: []string{"example.com"}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var buf [512]byte
		n, from, err := conn.ReadFromUDP(buf[:])
		if err != nil {
			return
		}
		query := buf[:n]
		answer := dns.ServFail(query)
		answer[3] &= 0xf0 // NOERROR
		_, _ = conn.WriteToUDP(answer, from)
		time.Sleep(100 * time.Millisecond)
		r.verifiedAnswer(dns.QName(query))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = r.verify(ctx, "example.com")
	<-done
	if err != nil {
		t.Fatal(err)
	}
}

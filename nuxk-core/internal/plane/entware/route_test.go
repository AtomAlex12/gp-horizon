package entware

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/dns"
)

type recordedDNS struct{ changes []string }

func (r *recordedDNS) ScopedNameServer(_ context.Context, _, name string, on bool) error {
	if on {
		r.changes = append(r.changes, "+"+name)
	} else {
		r.changes = append(r.changes, "-"+name)
	}
	return nil
}

func (r *recordedDNS) ScopedNameServers(context.Context, string) ([]string, error) {
	return []string{"keep.example", "remove.example", "other.example"}, nil
}

func TestReconcileOnlyChangesMissingAndOwnedDNS(t *testing.T) {
	rci := &recordedDNS{}
	r := Routing{RCI: rci}
	added, err := r.reconcileDNS(context.Background(), []string{"keep.example", "repair.example"},
		[]string{"keep.example", "repair.example", "remove.example", "already-absent.example"})
	if err != nil || !added || !reflect.DeepEqual(rci.changes, []string{"+repair.example", "-remove.example"}) {
		t.Fatalf("repair = %v, changes = %v, error = %v", added, rci.changes, err)
	}
}

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

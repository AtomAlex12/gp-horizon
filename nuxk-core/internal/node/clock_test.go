package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The router's clock against a reference's Date: 5 days off is seen and
// shown in Info.
func TestClockSkew(t *testing.T) {
	ref := time.Now().Add(-5 * 24 * time.Hour) // the reference: 5 days earlier than the box
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", ref.UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()
	c := NewClock()
	c.URL = srv.URL
	if _, _, ok := c.Skew(); ok {
		t.Fatal("known before a check")
	}
	c.Check(context.Background())
	skew, _, ok := c.Skew()
	if day5 := 5 * 24 * time.Hour; !ok || skew < day5-2*time.Second || skew > day5+2*time.Second {
		t.Fatalf("skew %v %v", skew, ok)
	}
	n := New("stand", "", "t", "c")
	n.Clock = c
	if in := n.Info(context.Background()); in.ClockSkewS < 431990 || in.ClockChecked == 0 {
		t.Fatalf("info %+v", in)
	}

	// unreachable: the last measurement stays
	c.URL = "http://127.0.0.1:1"
	c.Check(context.Background())
	if s2, _, ok := c.Skew(); !ok || s2 != skew {
		t.Fatalf("after a failed check %v %v", s2, ok)
	}
}

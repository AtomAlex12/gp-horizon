package dns

import (
	"reflect"
	"testing"
	"time"
)

func TestFrequent(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := New(Options{Clock: func() time.Time { return now }}, nil)
	ask := func(at time.Time, name string, n int) {
		for i := 0; i < n; i++ {
			s.rec.add(LogEntry{At: at.Unix(), Domain: name, Type: "A", Source: SrcUpstream})
		}
	}
	ask(now.Add(-2*time.Hour), "old.example", 9)       // two hours ago: its minute's bucket is reused below
	ask(now.Add(-100*time.Minute), "older.example", 9) // outside the hour: its bucket is stale
	ask(now, "youtube.com", 2)
	ask(now.Add(-30*time.Minute), "youtube.com", 2) // 4 within the hour
	ask(now, "openai.com", 3)
	ask(now, "rare.example", 2)

	if got := s.Frequent(time.Hour, 3); got != nil {
		t.Fatalf("protected DNS off must give nothing: %v", got)
	}
	s.set.Enabled = true
	if !s.Watching() {
		t.Fatal("watching")
	}
	if got, want := s.Frequent(time.Hour, 3), []string{"youtube.com", "openai.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hour: %v, want %v", got, want)
	}
	if got, want := s.Frequent(10*time.Minute, 2), []string{"openai.com", "rare.example", "youtube.com"}; !reflect.DeepEqual(got, want) {
		t.Errorf("10 min: %v, want %v", got, want)
	}
	if got := s.Frequent(3*time.Hour, 1); len(got) != 3 {
		t.Errorf("past the hour kept, only the hour counts: %v", got)
	}
}

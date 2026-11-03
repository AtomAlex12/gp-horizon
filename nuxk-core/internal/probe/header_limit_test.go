package probe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type countedReadConn struct {
	net.Conn
	bytes *atomic.Int64
}

func (c countedReadConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.bytes.Add(int64(n))
	return n, err
}

func TestHTTPResponseHeadersAreBounded(t *testing.T) {
	for _, kind := range []string{"status", "field", "many", "freeze"} {
		t.Run(kind, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "freeze" && r.Method == http.MethodGet {
					_, _ = io.WriteString(w, "ok")
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				conn, out, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				status, header := "OK", "X-Large: "+strings.Repeat("x", 192<<10)+"\r\n"
				if kind == "status" {
					status, header = strings.Repeat("x", 192<<10), ""
				} else if kind == "many" {
					header = strings.Repeat("X-Small: x\r\n", 20000)
				}
				_, _ = io.WriteString(out, "HTTP/1.1 200 "+status+"\r\n"+header+"Content-Length: 2\r\n\r\nok")
				_ = out.Flush()
			}))
			defer s.Close()
			p := prober(s, nil, nil)
			p.Samples, p.NoFreeze = 1, kind != "freeze"
			var received atomic.Int64
			dial := p.Dial
			p.Dial = func(ctx context.Context, via Via, address string) (net.Conn, error) {
				c, err := dial(ctx, via, address)
				if err != nil {
					return nil, err
				}
				return countedReadConn{Conn: c, bytes: &received}, nil
			}
			r := p.Way(context.Background(), "example.com", net.ParseIP("203.0.113.1"), ViaDirect)
			if r.Clean() || r.Frozen || r.Skipped == "" {
				t.Errorf("oversized headers became a valid measurement: %+v", r)
			}
			// Includes TLS overhead, and two connections in the freeze case.
			if n := received.Load(); n > 100<<10 {
				t.Errorf("read %d wire bytes before rejecting headers; header parsing is unbounded", n)
			}
		})
	}
}

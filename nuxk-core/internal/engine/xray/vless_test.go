package xray

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"nuxk.dev/horizon/core/internal/engine"
)

// as 3x-ui shares a VLESS + Reality + TCP (vision) inbound; the id and keys
// are made up
const realityLink = "vless://0e2b3c4d-1111-4a2b-9c3d-5e6f7a8b9c0d@203.0.113.9:443" +
	"?type=tcp&security=reality&pbk=Q1l8e0Xk3sB9m2yVbS7uPq4nHr6tGf5dJc8wA1zXy2E&fp=chrome" +
	"&sni=www.microsoft.com&sid=6ba85179e30d4fc2&spx=%2F&flow=xtls-rprx-vision#%D0%B4%D0%BE%D0%BC"

func TestParseReality(t *testing.T) {
	s, err := ParseVLESS(realityLink)
	if err != nil {
		t.Fatal(err)
	}
	want := Server{Name: "дом", ID: "0e2b3c4d-1111-4a2b-9c3d-5e6f7a8b9c0d", Host: "203.0.113.9", Port: 443,
		Flow: "xtls-rprx-vision", Network: "raw", Security: "reality", SNI: "www.microsoft.com", Fingerprint: "chrome",
		PublicKey: "Q1l8e0Xk3sB9m2yVbS7uPq4nHr6tGf5dJc8wA1zXy2E", ShortID: "6ba85179e30d4fc2", SpiderX: "/", Encryption: "none"}
	if s.Name != want.Name || s.ID != want.ID || s.Host != want.Host || s.Port != want.Port || s.Flow != want.Flow ||
		s.Network != want.Network || s.Security != want.Security || s.SNI != want.SNI || s.Fingerprint != want.Fingerprint ||
		s.PublicKey != want.PublicKey || s.ShortID != want.ShortID || s.SpiderX != want.SpiderX || s.Encryption != want.Encryption {
		t.Errorf("got %+v\nwant %+v", s, want)
	}
}

func TestParseRefusals(t *testing.T) {
	for name, link := range map[string]string{
		"not vless":         "vmess://abc@1.2.3.4:443",
		"no id":             "vless://1.2.3.4:443?security=reality",
		"no port":           strings.Replace(realityLink, ":443", "", 1),
		"bad port":          strings.Replace(realityLink, ":443", ":70000", 1),
		"reality w/o pbk":   strings.Replace(realityLink, "pbk=", "x=", 1),
		"reality w/o sni":   strings.Replace(realityLink, "sni=", "x=", 1),
		"unknown transport": strings.Replace(realityLink, "type=tcp", "type=kcp", 1),
		"unknown security":  strings.Replace(realityLink, "security=reality", "security=xtls", 1),
		"vision over ws":    strings.Replace(realityLink, "type=tcp", "type=ws", 1),
		"quotes in sni":     strings.Replace(realityLink, "sni=www.microsoft.com", "sni=a%22b", 1),
		"newline in fp":     strings.Replace(realityLink, "fp=chrome", "fp=chrome%0Aserver%20x", 1),
		"host is junk":      strings.Replace(realityLink, "203.0.113.9", "a_b", 1),
		"http header":       strings.Replace(realityLink, "#", "&headerType=http#", 1),
	} {
		if _, err := ParseVLESS(link); !errors.Is(err, engine.ErrBadConfig) {
			t.Errorf("%s: err = %v, want ErrBadConfig", name, err)
		} else if strings.Contains(err.Error(), "0e2b3c4d") {
			t.Errorf("%s: the error shows the id: %v", name, err)
		}
	}
}

func TestParseOthers(t *testing.T) {
	ws := "vless://id-1@example.com:443?type=ws&security=tls&sni=example.com&path=%2Fws&host=example.com&alpn=h2,http/1.1"
	s, err := ParseVLESS(ws)
	if err != nil || s.Network != "ws" || s.Path != "/ws" || len(s.ALPN) != 2 {
		t.Errorf("ws+tls: %+v %v", s, err)
	}
	if s, err := ParseVLESS("vless://id-2@[2001:db8::1]:8443?type=grpc&security=tls&serviceName=grpcsvc"); err != nil || s.Host != "2001:db8::1" || s.ServiceName != "grpcsvc" {
		t.Errorf("grpc on v6: %+v %v", s, err)
	}
	if s, err := ParseVLESS("vless://id-3@example.com:443?type=xhttp&security=reality&pbk=Q1l8e0Xk3sB9m2yVbS7uPq4nHr6tGf5dJc8wA1zXy2E&sni=example.com&path=%2Fx"); err != nil || s.Network != "xhttp" {
		t.Errorf("xhttp+reality: %+v %v", s, err)
	}
	if s, err := ParseVLESS("vless://id-4@1.2.3.4:80"); err != nil || s.Security != "none" || s.Network != "raw" {
		t.Errorf("plain: %+v %v", s, err)
	}
}

func TestRenderReality(t *testing.T) {
	s, _ := ParseVLESS(realityLink)
	b, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Inbounds  []any
		Outbounds []struct {
			Tag, Protocol string
			Settings      struct {
				Vnext []struct {
					Address string
					Port    int
					Users   []struct{ ID, Flow, Encryption string }
				}
			}
			StreamSettings struct {
				Network, Security string
				RealitySettings   struct{ ServerName, Fingerprint, PublicKey, ShortID, SpiderX string }
			}
		}
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.Inbounds != nil {
		t.Errorf("the TUN inbound is the script's (tun.json), not in config.json: %s", b)
	}
	out := c.Outbounds[0]
	v := out.Settings.Vnext[0]
	u := v.Users[0]
	rs := out.StreamSettings.RealitySettings
	if out.Protocol != "vless" || v.Address != "203.0.113.9" || v.Port != 443 || u.ID != s.ID || u.Flow != "xtls-rprx-vision" ||
		u.Encryption != "none" || out.StreamSettings.Network != "raw" || out.StreamSettings.Security != "reality" ||
		rs.ServerName != "www.microsoft.com" || rs.Fingerprint != "chrome" || rs.PublicKey != s.PublicKey ||
		rs.ShortID != "6ba85179e30d4fc2" || rs.SpiderX != "/" {
		t.Errorf("outbound: %s", b)
	}
	if c.Outbounds[1].Protocol != "freedom" {
		t.Errorf("a direct outbound after VLESS: %s", b)
	}
}

func TestMetaHasNoSecrets(t *testing.T) {
	s, _ := ParseVLESS(realityLink)
	m := meta(s, "203.0.113.9:443", "opkgtun1")
	if strings.Contains(m, s.ID) || strings.Contains(m, s.PublicKey) || strings.Contains(m, s.ShortID) {
		t.Errorf("meta shows secrets: %q", m)
	}
	for _, line := range strings.Split(strings.TrimSpace(m), "\n") {
		if k, v, ok := strings.Cut(line, " "); !ok || k == "" || v == "" || strings.ContainsAny(v, " \t") {
			t.Errorf("meta line %q: one key, one value", line)
		}
	}
}

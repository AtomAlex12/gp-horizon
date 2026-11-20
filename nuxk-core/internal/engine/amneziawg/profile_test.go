package amneziawg

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testProfile() string {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	return "[Interface]\nPrivateKey = " + key + "\nAddress = 172.16.0.2/32, 2606:4700:110::2/128\nDNS = 1.1.1.1\nTable = off\nMTU = 1280\nJc = 4\nI1 = <b 0x1234>\n[Peer]\nPublicKey = " + key + "\nEndpoint = 192.0.2.1:2408\nAllowedIPs = 0.0.0.0/0, ::/0\n"
}

func TestProfileKeepsOnlyDeviceAndPeerSettings(t *testing.T) {
	p, err := parseProfile(strings.NewReader(testProfile()))
	if err != nil {
		t.Fatal(err)
	}
	if p.IPv4.String() != "172.16.0.2/32" || p.MTU != 1280 || p.Endpoint.String() != "192.0.2.1:2408" {
		t.Fatalf("incorrect interface or endpoint: %v %d %v", p.IPv4, p.MTU, p.Endpoint)
	}
	for _, key := range []string{"Address", "DNS", "Table", "MTU"} {
		if strings.Contains(p.DeviceConfig, key+" =") {
			t.Fatalf("wg-quick setting %q passed to the device", key)
		}
	}
	for _, key := range []string{"PrivateKey", "Jc", "I1", "PublicKey", "AllowedIPs"} {
		if !strings.Contains(p.DeviceConfig, key+" =") {
			t.Fatalf("device setting %q lost", key)
		}
	}
}

func TestProfileRejectsHooksAndRedactsInvalidValues(t *testing.T) {
	for _, field := range []string{"PreUp", "PostUp", "PreDown", "PostDown", "SaveConfig", "PrivateKey", "I6"} {
		raw := strings.Replace(testProfile(), "[Interface]\n", "[Interface]\n"+field+" = sensitive-value\n", 1)
		_, err := parseProfile(strings.NewReader(raw))
		if err == nil || strings.Contains(err.Error(), "sensitive-value") {
			t.Fatalf("%s: expected redacted error, got %v", field, err)
		}
	}
}

func TestProfileRequiresLiteralEndpointAndIPv4DefaultAllowedIP(t *testing.T) {
	for old, replacement := range map[string]string{
		"192.0.2.1:2408": "example.net:2408",
		"0.0.0.0/0":      "192.0.2.0/24",
		"172.16.0.2/32":  "172.16.0.2/24",
	} {
		raw := strings.Replace(testProfile(), old, replacement, 1)
		if _, err := parseProfile(strings.NewReader(raw)); err == nil {
			t.Errorf("accepted %s", replacement)
		}
	}
}

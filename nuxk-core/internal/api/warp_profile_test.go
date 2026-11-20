package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nuxk.dev/horizon/core/internal/engine/amneziawg"
	"nuxk.dev/horizon/core/internal/engine/warp"
)

func TestWarpProfileImportKeepsKeysPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "warp-awg.conf")
	router := NewRouter(Deps{Token: "test-token", Warp: warp.New(nil, amneziawg.New("unused", path), "usque")})
	key := base64.StdEncoding.EncodeToString([]byte("fictional-key-test-material-1234"))
	config := "[Interface]\nPrivateKey = " + key +
		"\nAddress = 172.16.0.2/32\n[Peer]\nPublicKey = " + key +
		"\nEndpoint = 192.0.2.1:2408\nAllowedIPs = 0.0.0.0/0\n"
	upload := func(profile, token string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"config": profile})
		r := httptest.NewRequest(http.MethodPut, "/api/v1/warp/profile", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := upload(config, "invalid"); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized import: %d", w.Code)
	}
	if w := upload(config, "test-token"); w.Code != http.StatusOK || strings.Contains(w.Body.String(), key) {
		t.Fatalf("authorized import did not return safe success (%d)", w.Code)
	}
	if w := upload(config+"PostUp = sensitive-value\n", "test-token"); w.Code != http.StatusBadRequest ||
		strings.Contains(w.Body.String(), "sensitive-value") {
		t.Fatalf("invalid import did not return safe validation error (%d)", w.Code)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != config {
		t.Fatalf("valid profile was not preserved: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0600) {
		t.Fatalf("profile permissions: %v", err)
	}
}

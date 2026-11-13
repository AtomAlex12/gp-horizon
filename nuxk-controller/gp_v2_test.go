package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGPV2PasswordRotation(t *testing.T) {
	password := "admin"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/access/authentication", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["password"] != password {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"bearer_token":"new-token","expires_at":"2099-01-01T00:00:00Z"}`))
	})
	mux.HandleFunc("PUT /api/v2/access/change_password", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "admin" || body["old_password"] != password || r.Header.Get("Authorization") != "Bearer new-token" {
			w.WriteHeader(403)
			return
		}
		password = body["new_password"]
		w.WriteHeader(204)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := NewGPClient(server.URL, store, nil)
	client.v2 = true
	token, err := client.Token(context.Background())
	if err != nil || token != "new-token" || password == "admin" || store.PluginSecret("gp") != password {
		t.Fatalf("API 2 rotation failed: %v", err)
	}
}

func TestGPV2RoutePolicy(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		allowed      bool
	}{
		{"GET", "search/events", true},
		{"GET", "data/sources/v2fly/categories/geolocation-!cn/domains", true},
		{"GET", "data/strategies/42/archive", true},
		{"POST", "backup/restore-file", true},
		{"PUT", "access/change_password", false},
		{"POST", "service/updates", false},
		{"GET", "data/strategies/../archive", false},
	} {
		if gpV2Allowed(tc.method, tc.path) != tc.allowed {
			t.Errorf("%s %s", tc.method, tc.path)
		}
	}
}

func TestGPV2RollbackInvalidatesBearer(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	socket := fakeSupervisor(t, func(supReq) supResp {
		return supResp{OK: true, Plugins: []PluginInfo{{Name: "gp", Phase: "running", Version: "v0.4.4"}}}
	})
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := NewGPClient(server.URL, store, NewPluginHost(socket))
	client.v2 = true
	client.token = "stale"
	client.expires = time.Now().Add(time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/ctl/v1/gp2/service/status", nil)
	request.SetPathValue("path", "service/status")
	response := httptest.NewRecorder()
	client.handle(response, request)
	var body struct{ Error struct{ Code string } }
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != "gp_api_version" {
		t.Fatalf("rollback status: %d %s", response.Code, response.Body.String())
	}
}

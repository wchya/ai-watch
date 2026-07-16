package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-watch/internal/configscan"
	"ai-watch/internal/secureconfig"
	"ai-watch/internal/store"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestNotificationRoutingChannelCRUDRoutesAndTest(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	st := store.NewRedisWithClient(t.TempDir(), "routing-api", client, []byte("0123456789abcdef0123456789abcdef"))
	defer st.Close()
	secure := secureconfig.New(st, nil, "")
	requests := 0
	robot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer robot.Close()
	h := New(configscan.New(), nil, "", st).WithSecureConfig(secure).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	created := call(http.MethodPost, "/api/notification-channels", `{"id":"incident-room","name":"Incident Room","description":"critical incidents","type":"dingtalk","enabled":true,"webhookUrl":"`+robot.URL+`"}`)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), robot.URL) || !strings.Contains(created.Body.String(), `"configured":true`) {
		t.Fatalf("created=%d %s", created.Code, created.Body.String())
	}
	routes := call(http.MethodPut, "/api/notification-routes", `{"routes":{"incident_opened":"incident-room","incident_recovered":"","reliability_alert":"","reliability_recovered":"","reliability_digest":"","job_notification":""}}`)
	if routes.Code != http.StatusOK || !strings.Contains(routes.Body.String(), `"incident_opened":"incident-room"`) {
		t.Fatalf("routes=%d %s", routes.Code, routes.Body.String())
	}
	tested := call(http.MethodPost, "/api/notification-channels/incident-room/test", "")
	if tested.Code != http.StatusOK || requests != 1 {
		t.Fatalf("tested=%d %s requests=%d", tested.Code, tested.Body.String(), requests)
	}
	deleted := call(http.MethodDelete, "/api/notification-channels/incident-room", "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("deleted=%d %s", deleted.Code, deleted.Body.String())
	}
	loaded := call(http.MethodGet, "/api/notification-routes", "")
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), `"incident_opened":""`) {
		t.Fatalf("loaded=%d %s", loaded.Code, loaded.Body.String())
	}
}

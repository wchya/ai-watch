package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ai-watch/internal/configscan"
	"ai-watch/internal/store"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisManagementHandler(t *testing.T) (http.Handler, *store.Redis, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	redisStore := store.NewRedisWithClient(t.TempDir(), "test", redis.NewClient(&redis.Options{Addr: server.Addr()}), []byte("0123456789abcdef0123456789abcdef"))
	if err := redisStore.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = redisStore.Close() })
	return New(configscan.New(), nil, "", redisStore).Handler(), redisStore, server
}

func TestRedisManagementListsAndReadsCommonTypes(t *testing.T) {
	handler, _, server := newRedisManagementHandler(t)
	server.Set("outside:string", "hello")
	server.HSet("outside:hash", "region", "cn", "mode", "safe")
	server.RPush("outside:list", "a", "b")
	server.SAdd("outside:set", "x", "y")
	server.ZAdd("outside:zset", 2, "two")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/redis/keys?pattern=outside:*&limit=20", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{"outside:string", "outside:hash", "outside:list", "outside:set", "outside:zset"} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("missing %s in %s", expected, recorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	path := "/api/redis/keys/detail?key=" + url.QueryEscape("outside:hash") + "&limit=20"
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"field":"region"`) || !strings.Contains(recorder.Body.String(), `"version":"`) {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRedisManagementMutatesTTLAndDeletes(t *testing.T) {
	handler, _, server := newRedisManagementHandler(t)
	server.Set("outside:string", "before")
	version := redisDetailVersion(t, handler, "outside:string")

	response := redisJSONRequest(t, handler, http.MethodPut, "/api/redis/keys/value", map[string]any{
		"key": "outside:string", "operation": "string:set", "value": "after", "version": version, "confirmKey": "outside:string",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("mutate status=%d body=%s", response.Code, response.Body.String())
	}
	if value, err := server.Get("outside:string"); err != nil || value != "after" {
		t.Fatalf("value=%q err=%v", value, err)
	}

	version = redisDetailVersion(t, handler, "outside:string")
	ttl := int64(90)
	response = redisJSONRequest(t, handler, http.MethodPut, "/api/redis/keys/ttl", map[string]any{"key": "outside:string", "version": version, "confirmKey": "outside:string", "ttlSeconds": ttl})
	if response.Code != http.StatusOK || server.TTL("outside:string").Seconds() != 90 {
		t.Fatalf("ttl status=%d ttl=%s body=%s", response.Code, server.TTL("outside:string"), response.Body.String())
	}

	version = redisDetailVersion(t, handler, "outside:string")
	response = redisJSONRequest(t, handler, http.MethodDelete, "/api/redis/keys", map[string]any{"key": "outside:string", "version": version, "confirmKey": "outside:string"})
	if response.Code != http.StatusOK || server.Exists("outside:string") {
		t.Fatalf("delete status=%d exists=%v body=%s", response.Code, server.Exists("outside:string"), response.Body.String())
	}
}

func TestRedisManagementRejectsUnsafeBounds(t *testing.T) {
	handler, _, server := newRedisManagementHandler(t)
	server.Set("outside:string", "value")
	version := redisDetailVersion(t, handler, "outside:string")
	response := redisJSONRequest(t, handler, http.MethodPut, "/api/redis/keys/ttl", map[string]any{
		"key": "outside:string", "version": version, "confirmKey": "outside:string", "ttlSeconds": redisMaxTTL + 1,
	})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_ttl") {
		t.Fatalf("ttl bound status=%d body=%s", response.Code, response.Body.String())
	}

	recorder := httptest.NewRecorder()
	pattern := strings.Repeat("a", redisMaxPattern+1)
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/redis/keys?pattern="+pattern, nil))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "redis_pattern_too_long") {
		t.Fatalf("pattern bound status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRedisManagementRollsBackInvalidApplicationValue(t *testing.T) {
	handler, _, server := newRedisManagementHandler(t)
	before, err := server.Get("test:settings")
	if err != nil {
		t.Fatal(err)
	}
	version := redisDetailVersion(t, handler, "test:settings")
	response := redisJSONRequest(t, handler, http.MethodPut, "/api/redis/keys/value", map[string]any{
		"key": "test:settings", "operation": "string:set", "value": "not-json", "version": version, "confirmKey": "test:settings",
	})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "rolled back") {
		t.Fatalf("rollback status=%d body=%s", response.Code, response.Body.String())
	}
	after, err := server.Get("test:settings")
	if err != nil || after != before {
		t.Fatalf("settings were not restored: before=%q after=%q err=%v", before, after, err)
	}
}

func TestRedisManagementRejectsVersionConflict(t *testing.T) {
	handler, _, server := newRedisManagementHandler(t)
	server.Set("outside:string", "first")
	version := redisDetailVersion(t, handler, "outside:string")
	server.Set("outside:string", "second")
	response := redisJSONRequest(t, handler, http.MethodDelete, "/api/redis/keys", map[string]any{"key": "outside:string", "version": version, "confirmKey": "outside:string"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "redis_version_conflict") {
		t.Fatalf("conflict status=%d body=%s", response.Code, response.Body.String())
	}
}

func redisDetailVersion(t *testing.T, handler http.Handler, key string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/redis/keys/detail?key="+url.QueryEscape(key), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Version
}

func redisJSONRequest(t *testing.T, handler http.Handler, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	return recorder
}

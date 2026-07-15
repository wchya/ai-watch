package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/configscan"
	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
	"ai-watch/internal/runner"
	"ai-watch/internal/store"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type apiResolver struct{}

func (apiResolver) Resolve(_ domain.CLI, providerID string) (domain.ResolvedConfig, error) {
	return domain.ResolvedConfig{ProviderID: providerID, ProviderName: "Test Provider", BaseURL: "https://" + providerID + ".test/v1", LockIdentity: providerID}, nil
}

type apiExecutor struct{}

func (apiExecutor) Run(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
	return runner.Result{ExitCode: 0, Output: "READY"}, nil
}

type apiNotifier struct {
	messages chan string
	err      error
}

func (n *apiNotifier) Configured() bool                                               { return true }
func (n *apiNotifier) Notify(context.Context, domain.Job, domain.AttemptStatus) error { return n.err }
func (n *apiNotifier) Send(_ context.Context, title, content string) error {
	if n.err != nil {
		return n.err
	}
	n.messages <- title + "\n" + content
	return nil
}

func TestHealthAndSPAFallback(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>AI Watch</main>"), 0600)
	h := New(configscan.New(), nil, dir).Handler()
	for _, tc := range []struct{ path, contains string }{{"/api/health", `"status":"ok"`}, {"/jobs/abc", "AI Watch"}} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestHealthReturnsUnavailableWhenRedisStops(t *testing.T) {
	redisServer := miniredis.RunT(t)
	st := store.NewRedisWithClient(t.TempDir(), "health", redis.NewClient(&redis.Options{Addr: redisServer.Addr()}), []byte("0123456789abcdef0123456789abcdef"))
	if err := st.Err(); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := New(configscan.New(), nil, "", st).Handler()
	redisServer.Close()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"status":"degraded"`) {
		t.Fatalf("stopped Redis health=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSettingsAcceptReliabilityAlertFields(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st, nil)
	defer manager.Shutdown()
	handler := New(configscan.New(), manager, "", st).Handler()
	body := `{"reliabilityAlertEnabled":true,"reliabilityAlertMinSamples":9,"reliabilityAlertSuccessRate":85,"reliabilityAlertConsecutiveFailures":4,"reliabilityAlertP95Millis":1200,"reliabilityAlertCooldownSeconds":600,"reliabilityAlertRecoverySuccesses":3,"reliabilityAlertRecoveryEnabled":false,"uiTheme":"graphite-signal"}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var saved domain.Settings
	if err := json.Unmarshal(recorder.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.ReliabilityAlertEnabled || saved.ReliabilityAlertMinSamples != 9 || saved.ReliabilityAlertSuccessRate != 85 || saved.ReliabilityAlertConsecutiveFailures != 4 || saved.ReliabilityAlertP95Millis != 1200 || saved.ReliabilityAlertCooldownSeconds != 600 || saved.ReliabilityAlertRecoverySuccesses != 3 || saved.ReliabilityAlertRecoveryEnabled || saved.UITheme != domain.UIThemeGraphiteSignal {
		t.Fatalf("reliability settings were not saved: %+v", saved)
	}
}

func TestDiagnosticsIsReadOnlyAndDoesNotExposeSensitivePathsOrOutput(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "codex-safe")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'codex-cli 0.144.3\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "jobs", "temporary-secret-name"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_WATCH_RUNTIME_DIR", runtimeDir)
	st := store.New(filepath.Join(root, "data"))
	defer st.Close()
	scanner := &configscan.Scanner{CodexBin: bin, ClaudeBin: filepath.Join(root, "missing-claude")}
	h := New(scanner, nil, "", st).Handler()

	r := httptest.NewRequest(http.MethodGet, "/api/diagnostics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `"schemaVersion":10`) || !strings.Contains(body, `"pathLabel":"codex-safe"`) || !strings.Contains(body, `"directoryEntries":1`) {
		t.Fatalf("diagnostics status=%d body=%s", w.Code, body)
	}
	for _, forbidden := range []string{root, "temporary-secret-name", "webhook", "apiKey", "DINGTALK_WEBHOOK_URL"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("diagnostics exposed %q: %s", forbidden, body)
		}
	}
}

func TestNotificationTestAndStatusReuseConfiguredNotifier(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	n := &apiNotifier{messages: make(chan string, 2)}
	manager := jobs.New(apiResolver{}, apiExecutor{}, st, n)
	defer manager.Shutdown()
	h := New(configscan.New(), manager, "", st).Handler()
	for _, path := range []string{"/api/notifications/test", "/api/notifications/status"} {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sent":true`) {
			t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body.String())
		}
		select {
		case <-n.messages:
		case <-time.After(time.Second):
			t.Fatalf("%s did not use notifier", path)
		}
	}
}

func TestEventsListFilterAndClear(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	base := time.Date(2026, 7, 13, 6, 0, 0, 0, time.UTC)
	for _, event := range []store.Event{
		{At: base, Type: "job_state", Level: "info", ProviderID: "p1", JobID: "j1", Message: "started"},
		{At: base.Add(time.Minute), Type: "classification", Level: "success", ProviderID: "p1", JobID: "j1", Message: "success"},
		{At: base.Add(2 * time.Minute), Type: "cleanup", Level: "info", ProviderID: "p2", JobID: "j2", Message: "cleaned"},
	} {
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	h := New(configscan.New(), nil, "", st).Handler()

	r := httptest.NewRequest(http.MethodGet, "/api/events?providerId=p1&jobId=j1&level=info&since=2026-07-13T05:59:00Z&until=2026-07-13T06:01:00Z&limit=1&offset=0", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":1`) || !strings.Contains(w.Body.String(), `"providerId":"p1"`) {
		t.Fatalf("list events: status=%d body=%s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodDelete, "/api/events", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":3`) {
		t.Fatalf("clear events: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEventsFilterByScheduleID(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	now := time.Now().UTC()
	for _, event := range []store.Event{
		{At: now, Type: "request_end", JobID: "job-1", ScheduleID: "schedule-1", Data: map[string]any{"scheduleId": "schedule-1", "requestId": "request-1", "responseExcerpt": "READY"}},
		{At: now.Add(-time.Second), Type: "request_end", JobID: "job-2", ScheduleID: "schedule-2", Data: map[string]any{"scheduleId": "schedule-2", "requestId": "request-2"}},
	} {
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(configscan.New(), nil, "", st).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/events?scheduleId=schedule-1&type=request_end&limit=50", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"total":1`) || !strings.Contains(body, `"scheduleId":"schedule-1"`) || !strings.Contains(body, `"responseExcerpt":"READY"`) || strings.Contains(body, "request-2") {
		t.Fatalf("schedule events status=%d body=%s", recorder.Code, body)
	}
}

func TestReliabilityAggregatesRequestEventsAndRejectsInvalidRange(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	now := time.Now().UTC()
	for _, event := range []store.Event{
		{At: now.Add(-time.Hour), Type: "request_end", ProviderID: "ray", Data: map[string]any{"classification": "success", "durationMillis": 120, "job": map[string]any{"cli": "codex", "providerName": "Ray"}}},
		{At: now.Add(-30 * time.Minute), Type: "request_end", ProviderID: "ray", Data: map[string]any{"classification": "timeout", "durationMillis": 240, "responseExcerpt": "private-response-sample", "job": map[string]any{"cli": "codex", "providerName": "Ray"}}},
	} {
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(configscan.New(), nil, "", st).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/reliability?range=24h", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"sampleCount":2`) || !strings.Contains(body, `"successRate":0.5`) || !strings.Contains(body, `"key":"codex:ray"`) {
		t.Fatalf("reliability status=%d body=%s", recorder.Code, body)
	}
	for _, forbidden := range []string{"private-response-sample", `"responseExcerpt"`, `"prompt"`, `"maskedKey"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("reliability exposed %q: %s", forbidden, body)
		}
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/reliability?range=90d", nil))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_reliability_range") {
		t.Fatalf("invalid range status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRequestClientIPUsesRemoteAddrOnly(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/jobs", nil)
	r.RemoteAddr = "192.0.2.10:4321"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := requestClientIP(r); got != "192.0.2.10" {
		t.Fatalf("got %q", got)
	}
}

func TestEventsListPaginationAndValidation(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	base := time.Date(2026, 7, 13, 6, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		if err := st.SaveEvent(store.Event{At: base.Add(time.Duration(index) * time.Minute), Type: "job_state", Level: "info", JobID: fmt.Sprintf("j%d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	h := New(configscan.New(), nil, "", st).Handler()

	r := httptest.NewRequest(http.MethodGet, "/api/events?limit=1&offset=1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":3`) || !strings.Contains(w.Body.String(), `"jobId":"j1"`) {
		t.Fatalf("paged events: status=%d body=%s", w.Code, w.Body.String())
	}

	for _, path := range []string{
		"/api/events?limit=501",
		"/api/events?offset=-1",
		"/api/events?since=not-a-time",
		"/api/events?until=not-a-time",
		"/api/events?since=2026-07-13T07:00:00Z&until=2026-07-13T06:00:00Z",
	} {
		r = httptest.NewRequest(http.MethodGet, path, nil)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestProviderExamplesCRUDRejectsSecrets(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	h := New(configscan.New(), nil, "", st).Handler()

	r := httptest.NewRequest(http.MethodGet, "/api/provider-examples", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"codex-openai-compatible"`) || !strings.Contains(w.Body.String(), `"id":"claude-anthropic-compatible"`) {
		t.Fatalf("list provider examples: status=%d body=%s", w.Code, w.Body.String())
	}

	example := domain.ProviderExample{
		ID: "custom-codex", Name: "Custom Codex", CLI: domain.CLICodex,
		BaseURL: "https://example.test/v1", Model: "gpt-test", Provider: "custom",
		Description: "Non-sensitive template",
	}
	body, err := json.Marshal(example)
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodPost, "/api/provider-examples", strings.NewReader(string(body)))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"custom-codex"`) || !strings.Contains(w.Body.String(), `"updatedAt"`) {
		t.Fatalf("save provider example: status=%d body=%s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodPost, "/api/provider-examples", strings.NewReader(`{
		"id":"leaky","name":"Leaky","cli":"codex","baseUrl":"https://example.test","apiKey":"sk-abcdefghijklmnop"
	}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unknown field") {
		t.Fatalf("secret field was not rejected: status=%d body=%s", w.Code, w.Body.String())
	}
	examples, err := st.ListProviderExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 3 {
		t.Fatalf("invalid request changed provider examples: %+v", examples)
	}

	r = httptest.NewRequest(http.MethodDelete, "/api/provider-examples?id=custom-codex", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatalf("delete provider example: status=%d body=%s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodDelete, "/api/provider-examples?id=custom-codex", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing provider example status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSchedulesCRUDRejectsRuntimeSecretsAndBulkIsItemized(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	h := New(configscan.New(), manager, "", st).Handler()

	body := `{
		"name":"工作日测活","enabled":true,"cli":"codex","providerId":"provider-1",
		"mode":"probe","timezone":"Asia/Shanghai","weekdaysMask":62,
		"startMinute":540,"endMinute":1080,"untilSuccess":true,
		"timeoutSeconds":15,"retryIntervalSeconds":2,"keepaliveIntervalSeconds":120,
		"failureThreshold":3,"model":"gpt-test","fallbackModel":""
	}`
	r := httptest.NewRequest(http.MethodPost, "/api/schedules", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"nextRunAt"`) {
		t.Fatalf("create schedule: status=%d body=%s", w.Code, w.Body.String())
	}
	var created domain.Schedule
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode created schedule: value=%+v err=%v", created, err)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/schedules", strings.NewReader(strings.TrimSuffix(body, "}")+`,"apiKey":"sk-abcdefghijklmnop"}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unknown field") {
		t.Fatalf("schedule secret field was not rejected: status=%d body=%s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/schedules", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), created.ID) {
		t.Fatalf("list schedules: status=%d body=%s", w.Code, w.Body.String())
	}

	bulk := `{"action":"probe_once","items":[{"targetId":"provider-2","cli":"codex","providerId":"provider-2"}]}`
	r = httptest.NewRequest(http.MethodPost, "/api/jobs/bulk", strings.NewReader(bulk))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"accepted":1`) || !strings.Contains(w.Body.String(), `"failed":0`) || !strings.Contains(w.Body.String(), `"targetId":"provider-2"`) || !strings.Contains(w.Body.String(), `"runOnce":true`) {
		t.Fatalf("bulk jobs: status=%d body=%s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodDelete, "/api/schedules/"+created.ID, nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatalf("delete schedule: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestIdempotencyKeyReplaysAndRejectsConflicts(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	h := New(configscan.New(), manager, "", st).Handler()
	body := `{"name":"幂等计划","enabled":true,"cli":"codex","providerId":"provider-1","mode":"probe","timezone":"UTC","weekdaysMask":127,"startMinute":0,"endMinute":1439,"untilSuccess":true,"timeoutSeconds":15,"retryIntervalSeconds":2,"keepaliveIntervalSeconds":120,"failureThreshold":3}`
	call := func(payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/schedules", strings.NewReader(payload))
		r.Header.Set("Idempotency-Key", "test-idempotency-0001")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	responses := make(chan *httptest.ResponseRecorder, 2)
	go func() { responses <- call(body) }()
	go func() { responses <- call(body) }()
	first, second := <-responses, <-responses
	if first.Code != http.StatusCreated || second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("idempotent replay first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	list, err := st.ListSchedules()
	if err != nil || len(list) != 1 {
		t.Fatalf("duplicate schedule created: %+v err=%v", list, err)
	}
	conflict := call(strings.Replace(body, "幂等计划", "不同计划", 1))
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "idempotency_conflict") {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestScheduleAllowsCurrentProviderWithEmptyID(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	h := New(configscan.New(), manager, "", st).Handler()
	body := `{"name":"当前配置巡检","enabled":true,"cli":"codex","providerId":"","mode":"probe","timezone":"Asia/Shanghai","weekdaysMask":62,"startMinute":540,"endMinute":1080,"untilSuccess":true,"timeoutSeconds":15,"retryIntervalSeconds":2,"keepaliveIntervalSeconds":120,"failureThreshold":3}`
	r := httptest.NewRequest(http.MethodPost, "/api/schedules", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"providerId":""`) {
		t.Fatalf("current provider schedule status=%d body=%s", w.Code, w.Body.String())
	}
}

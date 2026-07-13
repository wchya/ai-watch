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
	if w.Code != http.StatusOK || !strings.Contains(body, `"schemaVersion":7`) || !strings.Contains(body, `"pathLabel":"codex-safe"`) || !strings.Contains(body, `"directoryEntries":1`) {
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

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/configscan"
	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

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

func TestEventsListFilterAndClear(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	for _, event := range []store.Event{
		{At: time.Now().UTC(), Type: "job_state", Level: "info", ProviderID: "p1", JobID: "j1", Message: "started"},
		{At: time.Now().UTC(), Type: "classification", Level: "success", ProviderID: "p2", JobID: "j2", Message: "success"},
	} {
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	h := New(configscan.New(), nil, "", st).Handler()

	r := httptest.NewRequest(http.MethodGet, "/api/events?providerId=p1&limit=50", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"total":1`) || !strings.Contains(w.Body.String(), `"providerId":"p1"`) {
		t.Fatalf("list events: status=%d body=%s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodDelete, "/api/events", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":2`) {
		t.Fatalf("clear events: status=%d body=%s", w.Code, w.Body.String())
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

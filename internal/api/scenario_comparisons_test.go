package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/configscan"
	"ai-watch/internal/jobs"
	"ai-watch/internal/store"
)

func TestScenarioComparisonCreatesBatchAndReturnsRequestFacts(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	handler := New(configscan.New(), manager, "", st).Handler()

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/api/scenario-comparisons", strings.NewReader(`{"scenarioId":"basic-ready","cli":"codex","providerIds":["provider-a"]}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}

	created := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/scenario-comparisons", strings.NewReader(`{"scenarioId":"basic-ready","cli":"codex","providerIds":["provider-a","provider-b"]}`))
	request.Header.Set("Idempotency-Key", "scenario-comparison-test")
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var batch scenarioComparison
	if err := json.Unmarshal(created.Body.Bytes(), &batch); err != nil {
		t.Fatal(err)
	}
	if batch.ID == "" || len(batch.Items) != 2 || strings.Contains(created.Body.String(), "只回复 READY") {
		t.Fatalf("unsafe or incomplete batch: %s", created.Body.String())
	}
	replayed := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/scenario-comparisons", strings.NewReader(`{"scenarioId":"basic-ready","cli":"codex","providerIds":["provider-a","provider-b"]}`))
	replayRequest.Header.Set("Idempotency-Key", "scenario-comparison-test")
	handler.ServeHTTP(replayed, replayRequest)
	if replayed.Code != http.StatusAccepted || replayed.Body.String() != created.Body.String() {
		t.Fatalf("idempotent replay status=%d body=%s want=%s", replayed.Code, replayed.Body.String(), created.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := manager.FlushEvents(); err != nil {
			t.Fatal(err)
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/api/scenario-comparisons/"+batch.ID, nil))
		if result.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", result.Code, result.Body.String())
		}
		var current scenarioComparison
		if err := json.Unmarshal(result.Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if current.Status == "completed" && len(current.Items) == 2 && current.Items[0].RequestID != "" && current.Items[1].RequestID != "" {
			for _, item := range current.Items {
				if item.Status != "success" || item.JobID == "" || item.RequestID == "" {
					t.Fatalf("item=%+v", item)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("comparison did not complete: %+v", current)
		}
		time.Sleep(20 * time.Millisecond)
	}
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/api/scenario-comparisons?status=completed", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), batch.ID) || !strings.Contains(listed.Body.String(), `"status":"completed"`) {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	rerun := httptest.NewRecorder()
	rerunRequest := httptest.NewRequest(http.MethodPost, "/api/scenario-comparisons/"+batch.ID+"/rerun", nil)
	rerunRequest.Header.Set("Idempotency-Key", "scenario-comparison-rerun-test")
	handler.ServeHTTP(rerun, rerunRequest)
	if rerun.Code != http.StatusAccepted || !strings.Contains(rerun.Body.String(), `"scenarioId":"basic-ready"`) || strings.Contains(rerun.Body.String(), `"id":"`+batch.ID+`"`) {
		t.Fatalf("rerun status=%d body=%s", rerun.Code, rerun.Body.String())
	}
}

func TestComparisonStatusDistinguishesRunningSuccessAndFailures(t *testing.T) {
	if got := comparisonStatus([]scenarioComparisonItem{{Status: "running"}, {Status: "failed"}}); got != "running" {
		t.Fatalf("running=%s", got)
	}
	if got := comparisonStatus([]scenarioComparisonItem{{Status: "success"}, {Status: "success"}}); got != "completed" {
		t.Fatalf("completed=%s", got)
	}
	if got := comparisonStatus([]scenarioComparisonItem{{Status: "success"}, {Status: "failed"}}); got != "partial_failed" {
		t.Fatalf("partial=%s", got)
	}
}

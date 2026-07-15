package reliability

import (
	"errors"
	"testing"
	"time"

	"ai-watch/internal/store"
)

type eventReader struct {
	events []store.Event
	err    error
}

func (r eventReader) ListEvents(filter store.EventFilter) ([]store.Event, error) {
	if r.err != nil {
		return nil, r.err
	}
	var values []store.Event
	for _, event := range r.events {
		if event.Type == filter.Type && !event.At.Before(filter.Since) && !event.At.After(filter.Until) {
			values = append(values, event)
		}
	}
	start := min(filter.Offset, len(values))
	end := min(start+filter.Limit, len(values))
	return values[start:end], nil
}

func requestEvent(at time.Time, cli, providerID, name, status string, duration int64) store.Event {
	return store.Event{At: at, Type: "request_end", ProviderID: providerID, Data: map[string]any{
		"classification": status, "durationMillis": float64(duration), "prompt": "must-not-leak",
		"job": map[string]any{"cli": cli, "providerName": name, "model": "model-a", "maskedKey": "sk-secret"},
	}}
}

func TestBuildAggregatesProvidersBucketsAndP95(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	events := []store.Event{
		requestEvent(now.Add(-5*time.Hour), "codex", "ray", "Ray", "success", 100),
		requestEvent(now.Add(-4*time.Hour), "codex", "ray", "Ray", "timeout", 200),
		requestEvent(now.Add(-3*time.Hour), "codex", "ray", "Ray", "overloaded", 300),
		requestEvent(now.Add(-2*time.Hour), "codex", "ray", "Ray", "success", 400),
		requestEvent(now.Add(-time.Hour), "codex", "ray", "Ray", "success", 500),
		requestEvent(now.Add(-30*time.Minute), "claude", "", "当前 Claude 配置", "stopped", 50),
	}
	result, err := Build(eventReader{events: events}, "24h", now, map[string]bool{"codex:ray": true, "claude:current": true}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Coverage.SampleCount != 6 || len(result.Buckets) != 24 || len(result.Providers) != 2 {
		t.Fatalf("unexpected response: %+v", result)
	}
	if result.Overall.AverageDurationMillis == nil || *result.Overall.AverageDurationMillis != 300 {
		t.Fatalf("stopped request affected latency: %+v", result.Overall.AverageDurationMillis)
	}
	ray := result.Providers[0]
	if ray.Key != "codex:ray" || ray.Metrics.Completed != 5 || ray.Metrics.Counts.Success != 3 || ray.Metrics.MaxConsecutiveFailures != 2 {
		t.Fatalf("ray metrics: %+v", ray)
	}
	if ray.Metrics.SuccessRate == nil || *ray.Metrics.SuccessRate != 0.6 {
		t.Fatalf("success rate: %+v", ray.Metrics.SuccessRate)
	}
	if ray.Metrics.P95DurationMillis == nil || *ray.Metrics.P95DurationMillis != 500 {
		t.Fatalf("p95: %+v", ray.Metrics.P95DurationMillis)
	}
	claude := result.Providers[1]
	if claude.Key != "claude:current" || claude.Metrics.Completed != 0 || claude.Metrics.Counts.Stopped != 1 {
		t.Fatalf("claude metrics: %+v", claude)
	}
}

func TestBuildMarksHistoricalAndPartialCoverage(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	result, err := Build(eventReader{events: []store.Event{requestEvent(now.Add(-time.Hour), "codex", "removed", "旧线路", "fatal", 10)}}, "30d", now, map[string]bool{}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Coverage.Partial || !result.Providers[0].Historical || result.Providers[0].LastFailureAt == nil {
		t.Fatalf("unexpected historical response: %+v", result)
	}
}

func TestBuildRejectsRangeAndPropagatesStoreError(t *testing.T) {
	if _, err := Build(eventReader{}, "90d", time.Now(), nil, 30); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("range err=%v", err)
	}
	want := errors.New("redis unavailable")
	if _, err := Build(eventReader{err: want}, "24h", time.Now(), nil, 30); !errors.Is(err, want) {
		t.Fatalf("store err=%v", err)
	}
}

func TestApplyRecommendations(t *testing.T) {
	rate := func(value float64) *float64 { return &value }
	p95 := func(value int64) *int64 { return &value }
	providers := []Provider{
		{Name: "主线路", Metrics: Metrics{Completed: 20, SuccessRate: rate(.98), P95DurationMillis: p95(900)}},
		{Name: "慢线路", Metrics: Metrics{Completed: 20, SuccessRate: rate(.92), P95DurationMillis: p95(2500)}},
		{Name: "故障线路", Metrics: Metrics{Completed: 10, SuccessRate: rate(.6), ConsecutiveFailures: 4}},
		{Name: "新线路", Metrics: Metrics{Completed: 2, SuccessRate: rate(1)}},
	}
	applyRecommendations(providers)
	want := []string{"recommended", "observe", "pause", "insufficient"}
	for index := range want {
		if providers[index].Recommendation.Level != want[index] {
			t.Fatalf("providers=%+v", providers)
		}
	}
}

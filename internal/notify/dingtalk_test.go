package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNotifyUsesSanitizedProbeTemplate(t *testing.T) {
	var content string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		content = body.Text.Content
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`)), Header: make(http.Header)}, nil
	})}

	start := time.Date(2026, 7, 13, 6, 37, 7, 0, time.UTC)
	end := start.Add(16 * time.Second)
	job := domain.Job{CLI: domain.CLICodex, ProviderID: "dadbbc58", ProviderName: "Ray", Provider: "custom", Model: "gpt-5.6-sol", Target: "http://newapi.raycloud.cn", MaskedKey: "sk-RtO...kfGw", Attempts: 3, StartedAt: start, EndedAt: &end, ElapsedMillis: 16000}
	notifier := New("https://example.invalid/robot")
	notifier.Client = client
	if err := notifier.Notify(context.Background(), job, domain.AttemptSuccess); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Codex 探活成功", "状态：已恢复可用 探测次数：3 总耗时：16s", "• 配置来源：CC Switch: Ray (dadbbc58)", "• provider：custom", "• apikey：sk-RtO...kfGw", "• 探测开始：2026-07-13 14:37:07"} {
		if !strings.Contains(content, want) {
			t.Fatalf("notification missing %q:\n%s", want, content)
		}
	}
}

func TestNotifyRejectsDingTalkApplicationError(t *testing.T) {
	notifier := New("https://example.invalid/robot")
	notifier.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"errcode":310000,"errmsg":"keywords not in content"}`)), Header: make(http.Header)}, nil
	})}
	err := notifier.Notify(context.Background(), domain.Job{CLI: domain.CLICodex, StartedAt: time.Now()}, domain.AttemptSuccess)
	if err == nil || !strings.Contains(err.Error(), "310000") {
		t.Fatalf("expected DingTalk application error, got %v", err)
	}
}

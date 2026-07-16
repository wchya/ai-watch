package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/runner"
	"ai-watch/internal/store"
)

type digestTestNotifier struct {
	messages chan string
	err      error
}

func (n *digestTestNotifier) Configured() bool { return true }
func (n *digestTestNotifier) Notify(context.Context, domain.Job, domain.AttemptStatus) error {
	return nil
}
func (n *digestTestNotifier) Send(_ context.Context, title, content string) error {
	if n.err != nil {
		return n.err
	}
	n.messages <- title + "\n" + content
	return nil
}

func newDigestTestManager(t *testing.T, notifier Notifier) (*Manager, *store.JSON) {
	t.Helper()
	st := store.New(t.TempDir())
	m := New(fakeResolver{}, execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{}, nil
	}), st, notifier)
	t.Cleanup(func() { m.Shutdown(); _ = st.Close() })
	return m, st
}

func seedDigestRequest(t *testing.T, st *store.JSON, at time.Time) {
	t.Helper()
	if err := st.SaveEvent(store.Event{At: at, Type: "request_end", ProviderID: "provider-a", JobID: "job-a", Data: map[string]any{
		"classification": "success", "durationMillis": 125,
		"job": map[string]any{"cli": "codex", "providerName": "Provider A"},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestReliabilityDigestPreviewDoesNotSendAndManualSendDoes(t *testing.T) {
	notifier := &digestTestNotifier{messages: make(chan string, 2)}
	m, st := newDigestTestManager(t, notifier)
	seedDigestRequest(t, st, time.Now().UTC().Add(-time.Minute))
	preview, err := m.ReliabilityDigestPreview()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Content, "整体成功率") || !strings.Contains(preview.Content, "Provider A") {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	select {
	case message := <-notifier.messages:
		t.Fatalf("preview sent notification: %s", message)
	default:
	}
	if _, err = m.SendReliabilityDigest(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-notifier.messages:
		if !strings.Contains(message, "AI Watch 可靠性摘要") {
			t.Fatalf("unexpected message: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("manual digest was not sent")
	}
}

func TestScheduledReliabilityDigestSendsOncePerDate(t *testing.T) {
	notifier := &digestTestNotifier{messages: make(chan string, 3)}
	m, st := newDigestTestManager(t, notifier)
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	seedDigestRequest(t, st, now.Add(-time.Minute))
	settings := m.Settings()
	settings.ReliabilityDigestEnabled = true
	settings.ReliabilityDigestHour = 0
	settings.ReliabilityDigestMinute = 0
	settings.ReliabilityDigestTimezone = "UTC"
	settings.ReliabilityDigestRange = "24h"
	if err := m.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := m.runReliabilityDigest(now); err != nil {
		t.Fatal(err)
	}
	if err := m.runReliabilityDigest(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifier.messages:
	default:
		t.Fatal("scheduled digest was not sent")
	}
	select {
	case message := <-notifier.messages:
		t.Fatalf("scheduled digest sent twice: %s", message)
	default:
	}
	if err := m.FlushEvents(); err != nil {
		t.Fatal(err)
	}
	count, err := st.CountEvents(store.EventFilter{Type: "reliability_digest_sent"})
	if err != nil || count != 1 {
		t.Fatalf("sent events=%d err=%v", count, err)
	}
}

func TestScheduledReliabilityDigestFailureCanRetry(t *testing.T) {
	notifier := &digestTestNotifier{messages: make(chan string, 1), err: errors.New("temporary delivery failure")}
	m, st := newDigestTestManager(t, notifier)
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	seedDigestRequest(t, st, now.Add(-time.Minute))
	settings := m.Settings()
	settings.ReliabilityDigestEnabled = true
	settings.ReliabilityDigestHour = 0
	settings.ReliabilityDigestTimezone = "UTC"
	if err := m.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := m.runReliabilityDigest(now); err == nil {
		t.Fatal("expected first delivery to fail")
	}
	notifier.err = nil
	if err := m.runReliabilityDigest(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifier.messages:
	case <-time.After(time.Second):
		t.Fatal("scheduled digest did not retry")
	}
	if err := m.FlushEvents(); err != nil {
		t.Fatal(err)
	}
	failed, err := st.CountEvents(store.EventFilter{Type: "reliability_digest_failed"})
	if err != nil || failed != 1 {
		t.Fatalf("failed events=%d err=%v", failed, err)
	}
}

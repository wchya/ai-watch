package jobs

import (
	"strings"
	"sync"
	"testing"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/reliability"
	"ai-watch/internal/store"
)

func reliabilityRequest(at time.Time, status string) store.Event {
	return store.Event{At: at, Type: "request_end", ProviderID: "ray", JobID: "job-1", Data: map[string]any{
		"classification": status, "durationMillis": 100,
		"job": map[string]any{"cli": "codex", "providerName": "Ray 主线路"},
	}}
}

func TestReliabilityAlertTriggersAndRecovers(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	notifier := &messageTestNotifier{messages: make(chan string, 4)}
	settings := domain.DefaultSettings()
	settings.ReliabilityAlertEnabled = true
	settings.ReliabilityAlertMinSamples = 100
	settings.ReliabilityAlertConsecutiveFailures = 3
	settings.ReliabilityAlertCooldownSeconds = 3600
	settings.ReliabilityAlertRecoverySuccesses = 2
	m := &Manager{store: st, notifier: notifier, settings: settings, notificationSlots: make(chan struct{}, 4)}
	now := time.Now().UTC()
	for index := 0; index < 3; index++ {
		event := reliabilityRequest(now.Add(time.Duration(index-2)*time.Minute), "timeout")
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
		if index == 2 {
			m.evaluateReliabilityAlert(event)
		}
	}
	select {
	case message := <-notifier.messages:
		if !strings.Contains(message, "连续失败 3 次") || !strings.Contains(message, "Ray 主线路") {
			t.Fatalf("alert=%s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("missing reliability alert")
	}
	for index := 1; index <= 2; index++ {
		event := reliabilityRequest(now.Add(time.Duration(index)*time.Minute), "success")
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
		m.evaluateReliabilityAlert(event)
	}
	select {
	case message := <-notifier.messages:
		if !strings.Contains(message, "可靠性恢复") {
			t.Fatalf("recovery=%s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("missing recovery notification")
	}
	triggered, err := st.CountEvents(store.EventFilter{Type: "reliability_alert_triggered"})
	if err != nil || triggered != 1 {
		t.Fatalf("triggered=%d err=%v", triggered, err)
	}
	recovered, err := st.CountEvents(store.EventFilter{Type: "reliability_alert_recovered"})
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
}

func TestReliabilityAlertReasonsRequireSamplesForRateAndP95(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.ReliabilityAlertMinSamples = 5
	settings.ReliabilityAlertSuccessRate = 90
	settings.ReliabilityAlertP95Millis = 1000
	rate := .5
	p95 := int64(2000)
	reasons := reliabilityAlertReasons(reliability.Metrics{Completed: 2, SuccessRate: &rate, P95DurationMillis: &p95, MaxConsecutiveFailures: 3, ConsecutiveFailures: 3}, settings)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "连续失败") {
		t.Fatalf("reasons=%v", reasons)
	}
}

func TestReliabilityAlertRepeatsAtConsecutiveFailureInterval(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	notifier := &messageTestNotifier{messages: make(chan string, 8)}
	settings := domain.DefaultSettings()
	settings.ReliabilityAlertEnabled = true
	settings.ReliabilityAlertMinSamples = 100
	settings.ReliabilityAlertConsecutiveFailures = 3
	settings.ReliabilityAlertCooldownSeconds = 3600
	m := &Manager{store: st, notifier: notifier, settings: settings, notificationSlots: make(chan struct{}, 4)}
	now := time.Now().UTC()

	for index := 1; index <= 6; index++ {
		event := reliabilityRequest(now.Add(time.Duration(index)*time.Minute), "timeout")
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
		m.evaluateReliabilityAlert(event)
	}

	for _, failures := range []string{"连续失败 3 次", "连续失败 6 次"} {
		select {
		case message := <-notifier.messages:
			if !strings.Contains(message, failures) || !strings.Contains(message, "每 3 次告警") {
				t.Fatalf("alert=%s", message)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing alert for %s", failures)
		}
	}
	select {
	case message := <-notifier.messages:
		t.Fatalf("unexpected alert=%s", message)
	default:
	}

	triggered, err := st.CountEvents(store.EventFilter{Type: "reliability_alert_triggered"})
	if err != nil || triggered != 2 {
		t.Fatalf("triggered=%d err=%v", triggered, err)
	}

	success := reliabilityRequest(now.Add(7*time.Minute), "success")
	if err := st.SaveEvent(success); err != nil {
		t.Fatal(err)
	}
	m.evaluateReliabilityAlert(success)
	for index := 1; index <= 2; index++ {
		event := reliabilityRequest(now.Add(time.Duration(7+index)*time.Minute), "timeout")
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
		m.evaluateReliabilityAlert(event)
	}
	select {
	case message := <-notifier.messages:
		t.Fatalf("unexpected alert after reset=%s", message)
	default:
	}
}

func TestReliabilityAlertFailureIntervalIsIdempotent(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	notifier := &messageTestNotifier{messages: make(chan string, 4)}
	settings := domain.DefaultSettings()
	settings.ReliabilityAlertEnabled = true
	settings.ReliabilityAlertMinSamples = 100
	settings.ReliabilityAlertConsecutiveFailures = 3
	settings.ReliabilityAlertCooldownSeconds = 3600
	m := &Manager{store: st, notifier: notifier, settings: settings, notificationSlots: make(chan struct{}, 4)}
	now := time.Now().UTC()
	var boundary store.Event
	for index := 1; index <= 3; index++ {
		boundary = reliabilityRequest(now.Add(time.Duration(index)*time.Minute), "timeout")
		if err := st.SaveEvent(boundary); err != nil {
			t.Fatal(err)
		}
	}

	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			m.evaluateReliabilityAlert(boundary)
		}()
	}
	wait.Wait()

	select {
	case message := <-notifier.messages:
		if !strings.Contains(message, "连续失败 3 次") {
			t.Fatalf("alert=%s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("missing reliability alert")
	}
	select {
	case message := <-notifier.messages:
		t.Fatalf("duplicate alert=%s", message)
	default:
	}
	triggered, err := st.CountEvents(store.EventFilter{Type: "reliability_alert_triggered"})
	if err != nil || triggered != 1 {
		t.Fatalf("triggered=%d err=%v", triggered, err)
	}
}

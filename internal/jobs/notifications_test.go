package jobs

import (
	"context"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/domain"
)

type messageTestNotifier struct {
	messages chan string
}

func (n *messageTestNotifier) Configured() bool { return true }
func (n *messageTestNotifier) Notify(context.Context, domain.Job, domain.AttemptStatus) error {
	return nil
}
func (n *messageTestNotifier) Send(_ context.Context, title, content string) error {
	n.messages <- title + "\n" + content
	return nil
}

func TestNotificationAggregationIsBoundedAndTriggeredByCount(t *testing.T) {
	n := &messageTestNotifier{messages: make(chan string, 2)}
	m := &Manager{
		notifier: n, notificationSlots: make(chan struct{}, 4),
		settings: domain.Settings{KeepaliveSummarySuccesses: 2},
	}
	job := domain.Job{CLI: domain.CLICodex, ProviderID: "provider-1", ProviderName: "Provider 1", Attempts: 2}
	m.recordKeepaliveSuccess(job)
	m.recordKeepaliveSuccess(job)
	select {
	case message := <-n.messages:
		if !strings.Contains(message, "AI Watch 保活汇总") || !strings.Contains(message, "本周期保活成功：**2** 次") {
			t.Fatalf("unexpected summary: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("keepalive summary was not sent")
	}

	var targets map[string]notificationTarget
	dropped := 0
	for index := 0; index < maxNotificationTargets+25; index++ {
		addNotificationTarget(&targets, &dropped, domain.Job{CLI: domain.CLICodex, ProviderID: string(rune(index + 1))})
	}
	if len(targets) != maxNotificationTargets || dropped != 25 {
		t.Fatalf("unbounded targets: len=%d dropped=%d", len(targets), dropped)
	}
	m.notificationWG.Wait()
}

func TestRecoveryMergesProvidersAndManualStatusUsesMessageNotifier(t *testing.T) {
	n := &messageTestNotifier{messages: make(chan string, 2)}
	m := &Manager{
		notifier: n, notificationSlots: make(chan struct{}, 4),
		settings: domain.Settings{RecoveryMergeSeconds: 1},
		jobs:     map[string]*runtime{},
	}
	m.queueRecovery(domain.Job{CLI: domain.CLICodex, ProviderID: "p1", ProviderName: "One", Attempts: 3})
	m.queueRecovery(domain.Job{CLI: domain.CLIClaude, ProviderID: "p2", ProviderName: "Two", Attempts: 4})
	select {
	case message := <-n.messages:
		if !strings.Contains(message, "恢复供应商：**2** 个") || !strings.Contains(message, "One") || !strings.Contains(message, "Two") {
			t.Fatalf("unexpected recovery summary: %s", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("merged recovery notification was not sent")
	}
	if err := m.SendStatusSummary(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-n.messages:
		if !strings.Contains(message, "AI Watch 状态汇总") {
			t.Fatalf("unexpected status summary: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("manual status summary was not sent")
	}
	m.notificationWG.Wait()
}

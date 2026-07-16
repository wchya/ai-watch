package store

import (
	"os"
	"strings"
	"testing"

	"ai-watch/internal/domain"
)

func TestSQLiteNotificationChannelEncryptsSecretAndRoutesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	defer st.Close()
	secret := "https://oapi.example/robot/send?access_token=sqlite-routing-secret"
	saved, err := st.UpsertNotificationChannel(domain.NotificationChannel{ID: "ops", Name: "Ops", Type: "dingtalk", Enabled: true, WebhookURL: secret})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetNotificationChannel("ops")
	if err != nil || loaded.WebhookURL != secret || strings.Contains(saved.MaskedWebhook, "routing-secret") {
		t.Fatalf("loaded=%+v saved=%+v err=%v", loaded, saved, err)
	}
	routes, err := st.SaveNotificationRoutes(domain.NotificationRoutes{Routes: map[string]string{"incident_opened": "ops"}})
	if err != nil || routes.Routes["incident_opened"] != "ops" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(st.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sqlite-routing-secret") {
		t.Fatal("SQLite persisted notification webhook in plaintext")
	}
}

func TestRedisNotificationChannelEncryptsSecretAndRoutesRoundTrip(t *testing.T) {
	st, server := newTestRedis(t)
	secret := "https://oapi.example/robot/send?access_token=redis-routing-secret"
	if _, err := st.UpsertNotificationChannel(domain.NotificationChannel{ID: "alerts", Name: "Alerts", Type: "dingtalk", Enabled: true, WebhookURL: secret}); err != nil {
		t.Fatal(err)
	}
	raw := server.HGet("test:notification-channels", "alerts")
	if strings.Contains(raw, "redis-routing-secret") {
		t.Fatal("Redis persisted notification webhook in plaintext")
	}
	loaded, err := st.GetNotificationChannel("alerts")
	if err != nil || loaded.WebhookURL != secret {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err = st.SaveNotificationRoutes(domain.NotificationRoutes{Routes: map[string]string{"reliability_alert": "alerts"}}); err != nil {
		t.Fatal(err)
	}
	routes, err := st.LoadNotificationRoutes()
	if err != nil || routes.Routes["reliability_alert"] != "alerts" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
}

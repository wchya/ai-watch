package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ai-watch/internal/api"
	"ai-watch/internal/configscan"
	"ai-watch/internal/jobs"
	"ai-watch/internal/notify"
	"ai-watch/internal/runner"
	"ai-watch/internal/store"
)

func main() {
	addr := getenv("AI_WATCH_ADDR", ":8787")
	dataDir := getenv("AI_WATCH_DATA_DIR", "/data")
	webDir := os.Getenv("AI_WATCH_WEB_DIR")
	scanner := configscan.New()
	executor := runner.New()
	if err := executor.CleanupRuntimeJobs(); err != nil {
		log.Fatalf("clean stale runtime jobs: %v", err)
	}
	st := store.New(dataDir)
	if err := st.Err(); err != nil {
		log.Fatalf("initialize SQLite store: %v", err)
	}
	defer st.Close()
	manager := jobs.New(scanner, executor, st, notify.New(os.Getenv("DINGTALK_WEBHOOK_URL")))
	settings := manager.Settings()
	if _, err := st.RetainEvents(store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes}); err != nil {
		log.Fatalf("apply startup event retention: %v", err)
	}
	defer manager.Shutdown()
	srv := &http.Server{Addr: addr, Handler: api.New(scanner, manager, webDir, st).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 70 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		manager.Shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	log.Printf("AI Watch listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

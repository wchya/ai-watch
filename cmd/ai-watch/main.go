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
	"ai-watch/internal/proxyconfig"
	"ai-watch/internal/runner"
	"ai-watch/internal/secureconfig"
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
	redisURL := os.Getenv("AI_WATCH_REDIS_URL")
	if redisURL == "" {
		log.Fatal("AI_WATCH_REDIS_URL is required")
	}
	st := store.NewRedis(dataDir, redisURL)
	if err := st.Err(); err != nil {
		log.Fatalf("initialize Redis store: %v", err)
	}
	defer st.Close()
	syncCCSwitchProviders(scanner, st)
	secureService := secureconfig.New(st, scanner, os.Getenv("DINGTALK_WEBHOOK_URL"))
	if err := secureService.ImportEnvironmentDingTalk(); err != nil {
		log.Fatalf("import DingTalk environment configuration: %v", err)
	}
	manager := jobs.New(secureService, executor, st, secureService)
	proxyTestURL := getenv("AI_WATCH_MIHOMO_TEST_URL", "https://www.gstatic.com/generate_204")
	proxyService := proxyconfig.New(
		st,
		proxyconfig.NewHTTPController(getenv("AI_WATCH_MIHOMO_CONTROLLER_URL", "http://mihomo:9090"), 10*time.Second),
		proxyconfig.NewHTTPProxyTester(getenv("AI_WATCH_DEFAULT_PROXY_URL", "http://mihomo:7890"), proxyTestURL, 12*time.Second),
		proxyconfig.Options{
			RuntimePath:           getenv("AI_WATCH_MIHOMO_RUNTIME_PATH", "/mihomo-config/runtime.yaml"),
			RuntimeControllerPath: getenv("AI_WATCH_MIHOMO_RUNTIME_CONTROLLER_PATH", "/root/.config/mihomo/runtime.yaml"),
			BaseControllerPath:    getenv("AI_WATCH_MIHOMO_BASE_CONTROLLER_PATH", "/root/.config/mihomo/config.yaml"),
			ProviderHealthURL:     proxyTestURL,
			GroupTestURL:          proxyTestURL,
		},
	)
	restoreContext, cancelRestore := context.WithTimeout(context.Background(), 15*time.Second)
	if err := proxyService.Restore(restoreContext); err != nil {
		log.Printf("restore Mihomo subscription configuration: %v", err)
	}
	cancelRestore()
	if deleted, err := st.DeleteEventsByType("request_log"); err != nil {
		log.Fatalf("remove legacy request log events: %v", err)
	} else if deleted > 0 {
		log.Printf("removed %d legacy request_log events from the operational ledger", deleted)
	}
	settings := manager.Settings()
	if _, err := st.RetainEvents(store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes}); err != nil {
		log.Fatalf("apply startup event retention: %v", err)
	}
	defer manager.Shutdown()
	srv := &http.Server{Addr: addr, Handler: api.New(scanner, manager, webDir, st).WithSecureConfig(secureService).WithProxyConfig(proxyService).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 70 * time.Second}
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

func syncCCSwitchProviders(scanner *configscan.Scanner, st *store.Redis) {
	now := time.Now().UTC()
	status, err := st.LoadCCSwitchSyncStatus()
	if err != nil {
		log.Printf("load CC Switch sync status: %v", err)
		status = store.CCSwitchSyncStatus{}
	}
	status.LastAttemptAt = now
	if cached, cacheErr := st.ListCCSwitchProviders(); cacheErr == nil {
		status.Count = len(cached)
	}
	if _, statErr := os.Stat(scanner.CCSwitchDB); statErr != nil {
		status.SourceAvailable = false
		status.Warning = "CC Switch startup source is unavailable"
		if !errors.Is(statErr, os.ErrNotExist) {
			log.Printf("inspect CC Switch startup source: %v", statErr)
		}
		if saveErr := st.SaveCCSwitchSyncStatus(status); saveErr != nil {
			log.Printf("save CC Switch sync status: %v", saveErr)
		}
		return
	}
	status.SourceAvailable = true
	providers, loadErr := scanner.LoadCCSwitchProviders()
	if loadErr != nil {
		status.Warning = "CC Switch startup sync failed"
		log.Printf("CC Switch startup sync failed; using Redis snapshot: %v", loadErr)
		if saveErr := st.SaveCCSwitchSyncStatus(status); saveErr != nil {
			log.Printf("save CC Switch sync status: %v", saveErr)
		}
		return
	}
	if replaceErr := st.ReplaceCCSwitchProviders(providers); replaceErr != nil {
		status.Warning = "CC Switch Redis snapshot replacement failed"
		log.Printf("replace CC Switch Redis snapshot; using previous snapshot: %v", replaceErr)
		if saveErr := st.SaveCCSwitchSyncStatus(status); saveErr != nil {
			log.Printf("save CC Switch sync status: %v", saveErr)
		}
		return
	}
	success := time.Now().UTC()
	status.LastSuccessAt = &success
	status.Count = len(providers)
	status.Warning = ""
	if saveErr := st.SaveCCSwitchSyncStatus(status); saveErr != nil {
		log.Printf("save CC Switch sync status: %v", saveErr)
	}
	log.Printf("CC Switch startup sync loaded %d providers into Redis", len(providers))
}

func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

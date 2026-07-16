package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"

	"ai-watch/internal/configscan"
	"ai-watch/internal/diagnostics"
	"ai-watch/internal/jobs"
	"ai-watch/internal/secureconfig"
	"ai-watch/internal/store"
)

type Server struct {
	scanner       *configscan.Scanner
	jobs          *jobs.Manager
	webDir        string
	store         store.Store
	redis         *store.Redis
	secure        *secureconfig.Service
	idempotencyMu sync.Mutex
	idempotency   map[string]store.IdempotencyRecord
}

func New(scanner *configscan.Scanner, manager *jobs.Manager, webDir string, stores ...store.Store) *Server {
	var eventStore store.Store
	if len(stores) > 0 {
		eventStore = stores[0]
	}
	redisStore, _ := eventStore.(*store.Redis)
	return &Server{scanner: scanner, jobs: manager, webDir: webDir, store: eventStore, redis: redisStore, idempotency: map[string]store.IdempotencyRecord{}}
}
func (s *Server) WithSecureConfig(service *secureconfig.Service) *Server {
	s.secure = service
	return s
}
func (s *Server) Handler() http.Handler {
	return recoverMiddleware(s.idempotencyMiddleware(http.HandlerFunc(s.route)))
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/health" && r.Method == http.MethodGet:
		s.health(w)
	case p == "/api/diagnostics" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, diagnostics.New(s.scanner, s.jobs, s.store, "").Snapshot(r.Context()))
	case p == "/api/config/status" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.scanner.Status())
	case p == "/api/providers" && r.Method == http.MethodGet:
		s.providers(w, r)
	case p == "/api/test-scenarios" && r.Method == http.MethodGet:
		s.testScenarios(w)
	case p == "/api/test-scenarios" && r.Method == http.MethodPost:
		s.upsertTestScenario(w, r)
	case p == "/api/test-scenarios" && r.Method == http.MethodDelete:
		s.deleteTestScenario(w, r)
	case p == "/api/scenario-comparisons" && r.Method == http.MethodPost:
		s.createScenarioComparison(w, r)
	case p == "/api/scenario-comparisons" && r.Method == http.MethodGet:
		s.scenarioComparisons(w, r)
	case strings.HasPrefix(p, "/api/scenario-comparisons/") && strings.HasSuffix(p, "/rerun") && r.Method == http.MethodPost:
		s.rerunScenarioComparison(w, r)
	case strings.HasPrefix(p, "/api/scenario-comparisons/") && r.Method == http.MethodGet:
		s.scenarioComparison(w, r)
	case p == "/api/provider-groups" && r.Method == http.MethodGet:
		s.providerGroups(w)
	case p == "/api/provider-groups" && r.Method == http.MethodPost:
		s.upsertProviderGroup(w, r)
	case p == "/api/provider-groups" && r.Method == http.MethodDelete:
		s.deleteProviderGroup(w, r)
	case p == "/api/maintenance-windows" && r.Method == http.MethodGet:
		s.maintenanceWindows(w)
	case strings.HasPrefix(p, "/api/maintenance-windows/"):
		s.maintenanceWindowRoute(w, r)
	case p == "/api/slos" && r.Method == http.MethodGet:
		s.slos(w)
	case strings.HasPrefix(p, "/api/slos/"):
		s.sloRoute(w, r)
	case strings.HasPrefix(p, "/api/provider-groups/") && strings.HasSuffix(p, "/evaluate") && r.Method == http.MethodPost:
		s.evaluateProviderGroup(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "/api/provider-groups/"), "/evaluate"))
	case strings.HasPrefix(p, "/api/provider-groups/") && strings.HasSuffix(p, "/apply-advice") && r.Method == http.MethodPost:
		s.applyProviderGroupAdvice(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "/api/provider-groups/"), "/apply-advice"))
	case p == "/api/incidents" && r.Method == http.MethodGet:
		s.incidents(w, r)
	case strings.HasPrefix(p, "/api/incidents/"):
		s.incidentRoute(w, r)
	case p == "/api/manual-providers" && r.Method == http.MethodGet:
		s.manualProviders(w)
	case p == "/api/manual-providers" && r.Method == http.MethodPost:
		s.createManualProvider(w, r)
	case strings.HasPrefix(p, "/api/manual-providers/"):
		s.manualProviderRoute(w, r)
	case p == "/api/jobs" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.jobs.List())
	case p == "/api/jobs" && r.Method == http.MethodPost:
		s.start(w, r)
	case p == "/api/jobs/bulk" && r.Method == http.MethodPost:
		s.bulkJobs(w, r)
	case strings.HasPrefix(p, "/api/jobs/"):
		s.jobRoute(w, r)
	case strings.HasPrefix(p, "/api/requests/") && r.Method == http.MethodGet:
		s.requestDetail(w, strings.TrimPrefix(p, "/api/requests/"))
	case p == "/api/schedules" && r.Method == http.MethodGet:
		s.schedules(w)
	case p == "/api/schedules" && r.Method == http.MethodPost:
		s.createSchedule(w, r)
	case strings.HasPrefix(p, "/api/schedules/"):
		s.scheduleRoute(w, r)
	case p == "/api/settings" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.jobs.Settings())
	case p == "/api/settings" && r.Method == http.MethodPut:
		s.settings(w, r)
	case p == "/api/reliability" && r.Method == http.MethodGet:
		s.reliability(w, r)
	case p == "/api/reliability/actions" && r.Method == http.MethodGet:
		s.reliabilityActionContext(w, r)
	case p == "/api/reliability/actions" && r.Method == http.MethodPost:
		s.reliabilityAction(w, r)
	case p == "/api/reliability/export" && r.Method == http.MethodGet:
		s.reliabilityExport(w, r)
	case p == "/api/reliability/digest/preview" && r.Method == http.MethodGet:
		s.reliabilityDigestPreview(w)
	case p == "/api/reliability/digest/send" && r.Method == http.MethodPost:
		s.reliabilityDigestSend(w, r)
	case p == "/api/events" && r.Method == http.MethodGet:
		s.operationalEvents(w, r)
	case p == "/api/events" && r.Method == http.MethodDelete:
		s.clearEvents(w)
	case p == "/api/notifications/test" && r.Method == http.MethodPost:
		s.notification(w, r)
	case p == "/api/notifications/dingtalk/config" && r.Method == http.MethodGet:
		s.dingTalkConfig(w)
	case p == "/api/notifications/dingtalk/config" && r.Method == http.MethodPut:
		s.saveDingTalkConfig(w, r)
	case p == "/api/notification-channels" && r.Method == http.MethodGet:
		s.notificationChannels(w)
	case p == "/api/notification-channels" && r.Method == http.MethodPost:
		s.createNotificationChannel(w, r)
	case strings.HasPrefix(p, "/api/notification-channels/"):
		s.notificationChannelRoute(w, r)
	case p == "/api/notification-routes" && r.Method == http.MethodGet:
		s.notificationRoutes(w)
	case p == "/api/notification-routes" && r.Method == http.MethodPut:
		s.saveNotificationRoutes(w, r)
	case strings.HasPrefix(p, "/api/"):
		writeError(w, 404, "not_found", "API endpoint not found")
	default:
		s.web(w, r)
	}
}

func (s *Server) web(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.webDir == "" {
		http.Error(w, "AI Watch backend is running", http.StatusNotFound)
		return
	}
	root := os.DirFS(s.webDir)
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	if st, e := fs.Stat(root, name); e == nil && !st.IsDir() {
		http.ServeFileFS(w, r, root, name)
		return
	}
	if _, e := fs.Stat(root, "index.html"); e == nil {
		http.ServeFileFS(w, r, root, "index.html")
		return
	}
	http.NotFound(w, r)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		writeError(w, 400, "invalid_json", e.Error())
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(w, 500, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

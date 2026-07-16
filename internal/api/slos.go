package api

import (
	"errors"
	"io/fs"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

type sloView struct {
	GroupID         string  `json:"groupId"`
	GroupName       string  `json:"groupName"`
	CLI             string  `json:"cli"`
	Enabled         bool    `json:"enabled"`
	TargetPercent   float64 `json:"targetPercent"`
	Window          string  `json:"window"`
	MinimumSamples  int     `json:"minimumSamples"`
	Status          string  `json:"status"`
	Samples         int     `json:"samples"`
	Successes       int     `json:"successes"`
	Failures        int     `json:"failures"`
	Excluded        int     `json:"excluded"`
	SuccessRate     float64 `json:"successRate"`
	AllowedFailures float64 `json:"allowedFailures"`
	RemainingBudget float64 `json:"remainingBudget"`
	ConsumedPercent float64 `json:"consumedPercent"`
	BurnRate        float64 `json:"burnRate"`
	WindowStartedAt string  `json:"windowStartedAt"`
}

func (s *Server) slos(w http.ResponseWriter) {
	groups, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	values, err := groups.ListProviderGroups()
	if err != nil {
		writeError(w, 500, "slos_read_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	result := make([]sloView, 0, len(values))
	for _, group := range values {
		view, calcErr := s.calculateSLO(group, now)
		if calcErr != nil {
			writeError(w, 500, "slo_events_read_failed", calcErr.Error())
			return
		}
		result = append(result, view)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return sloRank(result[i].Status) < sloRank(result[j].Status) || sloRank(result[i].Status) == sloRank(result[j].Status) && result[i].GroupName < result[j].GroupName
	})
	writeJSON(w, 200, result)
}

func (s *Server) sloRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/slos/"), "/")
	parts := strings.Split(path, "/")
	groupID, action := parts[0], "configure"
	if len(parts) == 2 {
		action = parts[1]
	}
	if groupID == "" || len(parts) > 2 {
		writeError(w, 404, "slo_operation_not_found", "slo operation not found")
		return
	}
	groups, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	group, err := groups.GetProviderGroup(groupID)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, 404, "provider_group_not_found", "provider group not found")
		return
	}
	if err != nil {
		writeError(w, 500, "provider_group_read_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	typ, message := "", ""
	switch {
	case action == "configure" && r.Method == http.MethodPut:
		var input struct {
			TargetPercent  float64 `json:"targetPercent"`
			Window         string  `json:"window"`
			MinimumSamples int     `json:"minimumSamples"`
		}
		if !decode(w, r, &input) {
			return
		}
		group.SLOEnabled, group.SLOTargetPercent, group.SLOWindow, group.SLOMinimumSamples = true, input.TargetPercent, input.Window, input.MinimumSamples
		typ, message = "slo_configured", "Provider Group SLO 已保存"
	case action == "pause" && r.Method == http.MethodPost:
		group.SLOEnabled = false
		typ, message = "slo_paused", "Provider Group SLO 已暂停"
	case action == "resume" && r.Method == http.MethodPost:
		group.SLOEnabled = true
		typ, message = "slo_resumed", "Provider Group SLO 已恢复"
	default:
		writeError(w, 404, "slo_operation_not_found", "slo operation not found")
		return
	}
	saved, err := groups.UpsertProviderGroup(group)
	if err != nil {
		writeError(w, 400, "slo_save_failed", err.Error())
		return
	}
	s.recordSLOEvent(now, typ, message, saved, requestClientIP(r))
	view, err := s.calculateSLO(saved, now)
	if err != nil {
		writeError(w, 500, "slo_events_read_failed", err.Error())
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) calculateSLO(group domain.ProviderGroup, now time.Time) (sloView, error) {
	if group.SLOTargetPercent == 0 {
		group.SLOTargetPercent = 99.9
	}
	if group.SLOWindow == "" {
		group.SLOWindow = "7d"
	}
	if group.SLOMinimumSamples == 0 {
		group.SLOMinimumSamples = 20
	}
	duration := 7 * 24 * time.Hour
	if group.SLOWindow == "24h" {
		duration = 24 * time.Hour
	} else if group.SLOWindow == "30d" {
		duration = 30 * 24 * time.Hour
	}
	view := sloView{GroupID: group.ID, GroupName: group.Name, CLI: string(group.CLI), Enabled: group.SLOEnabled, TargetPercent: group.SLOTargetPercent, Window: group.SLOWindow, MinimumSamples: group.SLOMinimumSamples, Status: "disabled", WindowStartedAt: now.Add(-duration).Format(time.RFC3339)}
	if !group.SLOEnabled {
		return view, nil
	}
	for offset := 0; ; offset += 1000 {
		events, err := s.store.ListEvents(store.EventFilter{Type: "request_end", Since: now.Add(-duration), Until: now, Limit: 1000, Offset: offset})
		if err != nil {
			return sloView{}, err
		}
		for _, event := range events {
			if stringValueAPI(event.Data["providerGroupId"]) != group.ID {
				continue
			}
			if booleanValue(event.Data["maintenanceActive"]) {
				view.Excluded++
				continue
			}
			status := strings.ToLower(stringValueAPI(event.Data["status"]))
			if status == "stopped" || status == "running" || status == "" {
				continue
			}
			view.Samples++
			if status == "success" {
				view.Successes++
			} else {
				view.Failures++
			}
		}
		if len(events) < 1000 {
			break
		}
	}
	if view.Samples > 0 {
		view.SuccessRate = float64(view.Successes) / float64(view.Samples) * 100
	}
	view.AllowedFailures = float64(view.Samples) * (1 - group.SLOTargetPercent/100)
	view.RemainingBudget = view.AllowedFailures - float64(view.Failures)
	if view.AllowedFailures > 0 {
		view.ConsumedPercent = float64(view.Failures) / view.AllowedFailures * 100
	}
	allowedRate := 1 - group.SLOTargetPercent/100
	if view.Samples > 0 && allowedRate > 0 {
		view.BurnRate = (float64(view.Failures) / float64(view.Samples)) / allowedRate
	}
	view.SuccessRate, view.AllowedFailures, view.RemainingBudget, view.ConsumedPercent, view.BurnRate = round(view.SuccessRate, 4), round(view.AllowedFailures, 4), round(view.RemainingBudget, 4), round(view.ConsumedPercent, 2), round(view.BurnRate, 2)
	view.Status = sloStatus(view, group.SLOMinimumSamples)
	return view, nil
}

func sloStatus(value sloView, minimum int) string {
	if !value.Enabled {
		return "disabled"
	}
	if value.Samples < minimum {
		return "insufficient"
	}
	if value.RemainingBudget <= 0 {
		return "exhausted"
	}
	if value.ConsumedPercent >= 90 || value.BurnRate >= 10 {
		return "critical"
	}
	if value.ConsumedPercent >= 50 || value.BurnRate >= 2 {
		return "burning"
	}
	return "healthy"
}
func sloRank(value string) int {
	switch value {
	case "exhausted":
		return 0
	case "critical":
		return 1
	case "burning":
		return 2
	case "healthy":
		return 3
	case "insufficient":
		return 4
	default:
		return 5
	}
}
func round(value float64, digits int) float64 {
	power := math.Pow10(digits)
	return math.Round(value*power) / power
}
func booleanValue(value any) bool { result, _ := value.(bool); return result }

func (s *Server) recordSLOEvent(at time.Time, typ, message string, group domain.ProviderGroup, clientIP string) {
	settings := s.jobs.Settings()
	_ = s.store.SaveEvent(store.Event{At: at, Type: typ, Level: "info", ProviderID: group.ActiveProviderID, Message: message, Data: map[string]any{"groupId": group.ID, "targetPercent": group.SLOTargetPercent, "window": group.SLOWindow, "minimumSamples": group.SLOMinimumSamples, "clientIP": clientIP}}, store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes})
}

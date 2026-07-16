package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func (s *Server) incidentStore(w http.ResponseWriter) (store.IncidentStore, bool) {
	values, ok := s.store.(store.IncidentStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "incidents_unavailable", "incident store is unavailable")
		return nil, false
	}
	return values, true
}
func (s *Server) incidents(w http.ResponseWriter, r *http.Request) {
	values, ok := s.incidentStore(w)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "open" && status != "acknowledged" && status != "muted" && status != "resolved" {
		writeError(w, http.StatusBadRequest, "invalid_incident_status", "invalid incident status")
		return
	}
	result, err := values.ListIncidents(status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "incidents_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (s *Server) incidentRoute(w http.ResponseWriter, r *http.Request) {
	values, ok := s.incidentStore(w)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/incidents/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "invalid_incident_id", "incident id is required")
		return
	}
	id := parts[0]
	if len(parts) >= 2 && parts[1] == "postmortem" {
		s.incidentPostmortemRoute(w, r, id, parts[2:])
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		value, err := values.GetIncident(id)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "incident_not_found", "incident not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "incident_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, value)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", "incident operation not found")
		return
	}
	value, err := values.GetIncident(id)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "incident_not_found", "incident not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "incident_read_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	action := parts[1]
	message := ""
	switch action {
	case "acknowledge":
		value.Status = "acknowledged"
		value.AcknowledgedAt = &now
		message = "事故已确认"
	case "note":
		var input struct {
			Note string `json:"note"`
		}
		if !decodeIncidentInput(w, r, &input) {
			return
		}
		value.Note = strings.TrimSpace(input.Note)
		message = "事故备注已更新"
	case "mute":
		var input struct {
			Seconds int `json:"seconds"`
		}
		if !decodeIncidentInput(w, r, &input) {
			return
		}
		if input.Seconds < 1 || input.Seconds > 604800 {
			writeError(w, http.StatusBadRequest, "invalid_mute_duration", "mute seconds must be 1..604800")
			return
		}
		until := now.Add(time.Duration(input.Seconds) * time.Second)
		value.MutedUntil = &until
		value.Status = "muted"
		message = "事故通知已静默"
	case "close":
		value.Status = "resolved"
		value.ResolvedAt = &now
		message = "事故已手动关闭"
	case "reopen":
		value.Status = "open"
		value.ResolvedAt = nil
		value.MutedUntil = nil
		message = "事故已重新打开"
	default:
		writeError(w, http.StatusNotFound, "not_found", "incident operation not found")
		return
	}
	value.Timeline = append(value.Timeline, domain.IncidentEntry{ID: time.Now().UTC().Format("20060102T150405.000000000"), At: now, Type: "manual_" + action, Message: message})
	saved, err := values.UpsertIncident(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "incident_update_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) incidentPostmortemRoute(w http.ResponseWriter, r *http.Request, incidentID string, rest []string) {
	incidents, ok := s.incidentStore(w)
	if !ok {
		return
	}
	postmortems, ok := s.store.(store.IncidentPostmortemStore)
	if !ok {
		writeError(w, 503, "postmortems_unavailable", "postmortem store is unavailable")
		return
	}
	if len(rest) == 1 && rest[0] == "markdown" && r.Method == http.MethodGet {
		value, err := postmortems.GetIncidentPostmortem(incidentID)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, 404, "postmortem_not_found", "postmortem not found")
			return
		}
		if err != nil {
			writeError(w, 500, "postmortem_read_failed", err.Error())
			return
		}
		body := incidentPostmortemMarkdown(value)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="incident-%s-postmortem.md"`, safeDownloadName(incidentID)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		return
	}
	if len(rest) == 0 && r.Method == http.MethodGet {
		value, err := postmortems.GetIncidentPostmortem(incidentID)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, 404, "postmortem_not_found", "postmortem not found")
			return
		}
		if err != nil {
			writeError(w, 500, "postmortem_read_failed", err.Error())
			return
		}
		writeJSON(w, 200, value)
		return
	}
	if len(rest) == 0 && r.Method == http.MethodPost {
		if existing, err := postmortems.GetIncidentPostmortem(incidentID); err == nil {
			writeJSON(w, 200, existing)
			return
		} else if !errors.Is(err, fs.ErrNotExist) {
			writeError(w, 500, "postmortem_read_failed", err.Error())
			return
		}
		incident, err := incidents.GetIncident(incidentID)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, 404, "incident_not_found", "incident not found")
			return
		}
		if err != nil {
			writeError(w, 500, "incident_read_failed", err.Error())
			return
		}
		value := postmortemFromIncident(incident, time.Now().UTC())
		saved, err := postmortems.UpsertIncidentPostmortem(value)
		if err != nil {
			writeError(w, 400, "postmortem_create_failed", err.Error())
			return
		}
		s.recordPostmortemEvent("incident_postmortem_created", "事故复盘草稿已生成", saved, requestClientIP(r))
		writeJSON(w, 201, saved)
		return
	}
	if len(rest) == 0 && r.Method == http.MethodPut {
		value, err := postmortems.GetIncidentPostmortem(incidentID)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, 404, "postmortem_not_found", "postmortem not found")
			return
		}
		if err != nil {
			writeError(w, 500, "postmortem_read_failed", err.Error())
			return
		}
		if value.Status == "completed" {
			writeError(w, 409, "postmortem_completed", "completed postmortem must be reopened before editing")
			return
		}
		var input struct {
			RootCause  string                    `json:"rootCause"`
			Mitigation string                    `json:"mitigation"`
			Owner      string                    `json:"owner"`
			Actions    []domain.PostmortemAction `json:"actions"`
		}
		if !decodeIncidentInput(w, r, &input) {
			return
		}
		value.RootCause, value.Mitigation, value.Owner, value.Actions = input.RootCause, input.Mitigation, input.Owner, input.Actions
		saved, err := postmortems.UpsertIncidentPostmortem(value)
		if err != nil {
			writeError(w, 400, "postmortem_save_failed", err.Error())
			return
		}
		s.recordPostmortemEvent("incident_postmortem_saved", "事故复盘草稿已保存", saved, requestClientIP(r))
		writeJSON(w, 200, saved)
		return
	}
	if len(rest) == 1 && (rest[0] == "complete" || rest[0] == "reopen") && r.Method == http.MethodPost {
		value, err := postmortems.GetIncidentPostmortem(incidentID)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, 404, "postmortem_not_found", "postmortem not found")
			return
		}
		if err != nil {
			writeError(w, 500, "postmortem_read_failed", err.Error())
			return
		}
		now := time.Now().UTC()
		typ, message := "incident_postmortem_completed", "事故复盘已完成"
		if rest[0] == "complete" {
			if value.Status == "completed" {
				writeJSON(w, 200, value)
				return
			}
			value.Status, value.CompletedAt = "completed", &now
		} else {
			if value.Status == "draft" {
				writeJSON(w, 200, value)
				return
			}
			value.Status, value.CompletedAt, typ, message = "draft", nil, "incident_postmortem_reopened", "事故复盘已重新打开"
		}
		saved, err := postmortems.UpsertIncidentPostmortem(value)
		if err != nil {
			writeError(w, 400, "postmortem_save_failed", err.Error())
			return
		}
		s.recordPostmortemEvent(typ, message, saved, requestClientIP(r))
		writeJSON(w, 200, saved)
		return
	}
	writeError(w, 404, "postmortem_operation_not_found", "postmortem operation not found")
}

func postmortemFromIncident(value domain.Incident, now time.Time) domain.IncidentPostmortem {
	end := now
	if value.ResolvedAt != nil {
		end = value.ResolvedAt.UTC()
	}
	timeline := append([]domain.IncidentEntry(nil), value.Timeline...)
	sort.SliceStable(timeline, func(i, j int) bool { return timeline[i].At.Before(timeline[j].At) })
	recovery := "事故尚未恢复"
	if value.ResolvedAt != nil {
		recovery = "事故已恢复并关闭"
	}
	for index := len(timeline) - 1; index >= 0; index-- {
		if strings.Contains(timeline[index].Type, "recover") || strings.Contains(timeline[index].Type, "resolved") || strings.Contains(timeline[index].Type, "close") {
			recovery = timeline[index].Message
			break
		}
	}
	return domain.IncidentPostmortem{IncidentID: value.ID, Status: "draft", Title: value.Title, Subject: firstNonEmpty(value.SubjectName, value.SubjectID), Severity: value.Severity, StartedAt: value.StartedAt, ResolvedAt: value.ResolvedAt, DurationSeconds: int64(max(0, int(end.Sub(value.StartedAt).Seconds()))), FailureCount: value.FailureCount, ErrorCounts: value.ErrorCounts, JobIDs: append([]string(nil), value.JobIDs...), RequestIDs: append([]string(nil), value.RequestIDs...), Timeline: timeline, RecoverySummary: recovery, RootCause: "待补充根因分析", Mitigation: firstNonEmpty(value.Note, "待补充处置总结"), Actions: []domain.PostmortemAction{}}
}

func incidentPostmortemMarkdown(value domain.IncidentPostmortem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 事故复盘：%s\n\n", value.Title)
	fmt.Fprintf(&b, "- 状态：%s\n- 影响对象：%s\n- 严重程度：%s\n- 开始时间：%s\n- 持续时间：%d 秒\n- 失败次数：%d\n\n", value.Status, value.Subject, value.Severity, value.StartedAt.Format(time.RFC3339), value.DurationSeconds, value.FailureCount)
	fmt.Fprintf(&b, "## 根因\n\n%s\n\n## 处置与恢复\n\n%s\n\n恢复结论：%s\n\n", value.RootCause, value.Mitigation, value.RecoverySummary)
	b.WriteString("## 失败分类\n\n")
	keys := make([]string, 0, len(value.ErrorCounts))
	for key := range value.ErrorCounts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&b, "- %s：%d\n", key, value.ErrorCounts[key])
	}
	b.WriteString("\n## 关键时间线\n\n")
	for _, entry := range value.Timeline {
		fmt.Fprintf(&b, "- %s · %s\n", entry.At.Format(time.RFC3339), entry.Message)
	}
	b.WriteString("\n## 后续行动\n\n")
	if len(value.Actions) == 0 {
		b.WriteString("- [ ] 待补充\n")
	} else {
		for _, action := range value.Actions {
			mark := " "
			if action.Completed {
				mark = "x"
			}
			fmt.Fprintf(&b, "- [%s] %s", mark, action.Text)
			if action.Owner != "" {
				fmt.Fprintf(&b, "（%s）", action.Owner)
			}
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "\n负责人：%s\n\n## 关联证据\n\n- Request IDs：%s\n- Job IDs：%s\n", firstNonEmpty(value.Owner, "待指定"), strings.Join(value.RequestIDs, "、"), strings.Join(value.JobIDs, "、"))
	return b.String()
}

func safeDownloadName(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, value)
}
func (s *Server) recordPostmortemEvent(typ, message string, value domain.IncidentPostmortem, clientIP string) {
	retention := store.EventRetention{MaxAge: 30 * 24 * time.Hour, MaxRows: 50000, MaxBytes: 128 << 20}
	if s.jobs != nil {
		settings := s.jobs.Settings()
		retention = store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes}
	}
	_ = s.store.SaveEvent(store.Event{At: time.Now().UTC(), Type: typ, Level: "info", Message: message, Data: map[string]any{"incidentId": value.IncidentID, "status": value.Status, "clientIP": clientIP}}, retention)
}
func decodeIncidentInput(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_incident_operation", "invalid incident operation payload")
		return false
	}
	return true
}

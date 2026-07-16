package api

import (
	"errors"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

type maintenanceWindow struct {
	GroupID             string     `json:"groupId"`
	GroupName           string     `json:"groupName"`
	CLI                 domain.CLI `json:"cli"`
	Mode                string     `json:"mode"`
	ActiveProviderID    string     `json:"activeProviderId"`
	MaintenanceStartsAt *time.Time `json:"maintenanceStartsAt,omitempty"`
	MaintenanceUntil    *time.Time `json:"maintenanceUntil,omitempty"`
	Status              string     `json:"status"`
	NotificationsMuted  bool       `json:"notificationsMuted"`
	FailoverSuppressed  bool       `json:"failoverSuppressed"`
}

func (s *Server) maintenanceWindows(w http.ResponseWriter) {
	groups, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	values, err := groups.ListProviderGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "maintenance_windows_read_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	result := make([]maintenanceWindow, 0, len(values))
	for _, group := range values {
		result = append(result, maintenanceWindowFromGroup(group, now))
	}
	sort.SliceStable(result, func(i, j int) bool {
		rank := func(status string) int {
			if status == "active" {
				return 0
			}
			if status == "scheduled" {
				return 1
			}
			if status == "ended" {
				return 2
			}
			return 3
		}
		if rank(result[i].Status) != rank(result[j].Status) {
			return rank(result[i].Status) < rank(result[j].Status)
		}
		return result[i].GroupName < result[j].GroupName
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) maintenanceWindowRoute(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "maintenance_windows_unavailable", "maintenance windows are unavailable")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/maintenance-windows/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "maintenance_operation_not_found", "maintenance operation not found")
		return
	}
	groups, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	group, err := groups.GetProviderGroup(parts[0])
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "provider_group_not_found", "provider group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider_group_read_failed", err.Error())
		return
	}
	now := time.Now().UTC()
	typ, message := "", ""
	switch parts[1] {
	case "start":
		var input struct {
			StartsAt *time.Time `json:"startsAt"`
			Until    time.Time  `json:"until"`
		}
		if !decode(w, r, &input) {
			return
		}
		startsAt := now
		if input.StartsAt != nil {
			startsAt = input.StartsAt.UTC()
		}
		until := input.Until.UTC()
		if startsAt.Before(now.Add(-time.Minute)) || startsAt.After(now.Add(365*24*time.Hour)) || !until.After(startsAt) || until.After(startsAt.Add(30*24*time.Hour)) {
			writeError(w, http.StatusBadRequest, "invalid_maintenance_window", "maintenance window must start within one year and last no more than 30 days")
			return
		}
		group.MaintenanceStartsAt, group.MaintenanceUntil = &startsAt, &until
		typ, message = "maintenance_started", "ProviderGroup 维护窗口已设置"
	case "extend":
		var input struct {
			Seconds int `json:"seconds"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Seconds < 60 || input.Seconds > 30*24*3600 {
			writeError(w, http.StatusBadRequest, "invalid_maintenance_extension", "extension seconds must be 60..2592000")
			return
		}
		base := now
		if group.MaintenanceUntil != nil && group.MaintenanceUntil.After(base) {
			base = group.MaintenanceUntil.UTC()
		}
		until := base.Add(time.Duration(input.Seconds) * time.Second)
		startsAt := now
		if group.MaintenanceStartsAt != nil {
			startsAt = group.MaintenanceStartsAt.UTC()
		}
		if until.After(startsAt.Add(30 * 24 * time.Hour)) {
			writeError(w, http.StatusBadRequest, "invalid_maintenance_extension", "maintenance window must last no more than 30 days")
			return
		}
		group.MaintenanceStartsAt, group.MaintenanceUntil = &startsAt, &until
		typ, message = "maintenance_extended", "ProviderGroup 维护窗口已延长"
	case "end":
		group.MaintenanceStartsAt, group.MaintenanceUntil = nil, nil
		typ, message = "maintenance_ended", "ProviderGroup 维护窗口已提前结束"
	default:
		writeError(w, http.StatusNotFound, "maintenance_operation_not_found", "maintenance operation not found")
		return
	}
	saved, err := groups.UpsertProviderGroup(group)
	if err != nil {
		writeError(w, http.StatusBadRequest, "maintenance_window_save_failed", err.Error())
		return
	}
	s.syncIncidentMaintenance(saved)
	s.recordMaintenanceEvent(now, typ, message, saved, requestClientIP(r))
	s.jobs.RecordProviderGroupMaintenance(saved.ID, typ, message, now)
	writeJSON(w, http.StatusOK, maintenanceWindowFromGroup(saved, now))
}

func maintenanceWindowFromGroup(group domain.ProviderGroup, now time.Time) maintenanceWindow {
	status := "none"
	if group.MaintenanceUntil != nil {
		if !group.MaintenanceUntil.After(now) {
			status = "ended"
		} else if group.MaintenanceStartsAt != nil && group.MaintenanceStartsAt.After(now) {
			status = "scheduled"
		} else {
			status = "active"
		}
	}
	active := status == "active"
	return maintenanceWindow{GroupID: group.ID, GroupName: group.Name, CLI: group.CLI, Mode: group.Mode, ActiveProviderID: group.ActiveProviderID, MaintenanceStartsAt: group.MaintenanceStartsAt, MaintenanceUntil: group.MaintenanceUntil, Status: status, NotificationsMuted: active, FailoverSuppressed: active}
}

func (s *Server) syncIncidentMaintenance(group domain.ProviderGroup) {
	incidents, ok := s.store.(store.IncidentStore)
	if !ok {
		return
	}
	incident, err := incidents.FindOpenIncident("group", group.ID)
	if err != nil {
		return
	}
	incident.MaintenanceStartsAt, incident.MaintenanceUntil = group.MaintenanceStartsAt, group.MaintenanceUntil
	_, _ = incidents.UpsertIncident(incident)
}

func (s *Server) recordMaintenanceEvent(at time.Time, typ, message string, group domain.ProviderGroup, clientIP string) {
	settings := s.jobs.Settings()
	_ = s.store.SaveEvent(store.Event{At: at, Type: typ, Level: "info", ProviderID: group.ActiveProviderID, Message: message, Data: map[string]any{"groupId": group.ID, "maintenanceStartsAt": group.MaintenanceStartsAt, "maintenanceUntil": group.MaintenanceUntil, "clientIP": clientIP}}, store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes})
}

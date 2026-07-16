package api

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func (s *Server) notificationChannels(w http.ResponseWriter) {
	if s.secure == nil {
		writeError(w, 503, "notification_routing_unavailable", "notification routing is unavailable")
		return
	}
	values, err := s.secure.ListNotificationChannels()
	if err != nil {
		writeError(w, 500, "notification_channels_read_failed", err.Error())
		return
	}
	writeJSON(w, 200, values)
}
func (s *Server) createNotificationChannel(w http.ResponseWriter, r *http.Request) {
	if s.secure == nil {
		writeError(w, 503, "notification_routing_unavailable", "notification routing is unavailable")
		return
	}
	var input domain.NotificationChannelWrite
	if !decode(w, r, &input) {
		return
	}
	value, err := s.secure.SaveNotificationChannel(input, "")
	if err != nil {
		notificationRoutingError(w, err)
		return
	}
	s.recordNotificationRoutingChange("notification_channel_created", "通知渠道已创建", value.ID, requestClientIP(r))
	writeJSON(w, 201, value)
}
func (s *Server) notificationChannelRoute(w http.ResponseWriter, r *http.Request) {
	if s.secure == nil {
		writeError(w, 503, "notification_routing_unavailable", "notification routing is unavailable")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/notification-channels/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, 404, "notification_channel_not_found", "notification channel not found")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "test" && r.Method == http.MethodPost {
		if err := s.secure.TestNotificationChannel(r.Context(), id); err != nil {
			notificationRoutingError(w, err)
			return
		}
		s.recordNotificationRoutingChange("notification_channel_tested", "通知渠道测试成功", id, requestClientIP(r))
		writeJSON(w, 200, map[string]any{"sent": true, "id": id})
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, "notification_channel_operation_not_found", "notification channel operation not found")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input domain.NotificationChannelWrite
		if !decode(w, r, &input) {
			return
		}
		value, err := s.secure.SaveNotificationChannel(input, id)
		if err != nil {
			notificationRoutingError(w, err)
			return
		}
		s.recordNotificationRoutingChange("notification_channel_updated", "通知渠道已更新", id, requestClientIP(r))
		writeJSON(w, 200, value)
	case http.MethodDelete:
		deleted, err := s.secure.DeleteNotificationChannel(id)
		if err != nil {
			notificationRoutingError(w, err)
			return
		}
		if !deleted {
			writeError(w, 404, "notification_channel_not_found", "notification channel not found")
			return
		}
		s.recordNotificationRoutingChange("notification_channel_deleted", "通知渠道已删除，相关路由已回退默认", id, requestClientIP(r))
		writeJSON(w, 200, map[string]any{"deleted": true, "id": id})
	default:
		writeError(w, 404, "notification_channel_operation_not_found", "notification channel operation not found")
	}
}
func (s *Server) notificationRoutes(w http.ResponseWriter) {
	if s.secure == nil {
		writeError(w, 503, "notification_routing_unavailable", "notification routing is unavailable")
		return
	}
	value, err := s.secure.NotificationRoutes()
	if err != nil {
		writeError(w, 500, "notification_routes_read_failed", err.Error())
		return
	}
	writeJSON(w, 200, value)
}
func (s *Server) saveNotificationRoutes(w http.ResponseWriter, r *http.Request) {
	if s.secure == nil {
		writeError(w, 503, "notification_routing_unavailable", "notification routing is unavailable")
		return
	}
	var input struct {
		Routes map[string]string `json:"routes"`
	}
	if !decode(w, r, &input) {
		return
	}
	value, err := s.secure.SaveNotificationRoutes(input.Routes)
	if err != nil {
		notificationRoutingError(w, err)
		return
	}
	s.recordNotificationRoutingChange("notification_routes_updated", "通知路由已保存", "", requestClientIP(r))
	writeJSON(w, 200, value)
}
func notificationRoutingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, 404, "notification_channel_not_found", "notification channel not found")
	case strings.Contains(strings.ToLower(err.Error()), "invalid") || strings.Contains(strings.ToLower(err.Error()), "required"):
		writeError(w, 400, "invalid_notification_channel", err.Error())
	case strings.Contains(strings.ToLower(err.Error()), "disabled"):
		writeError(w, 409, "notification_channel_disabled", err.Error())
	default:
		writeError(w, 502, "notification_routing_failed", err.Error())
	}
}
func (s *Server) recordNotificationRoutingChange(typ, message, channelID, clientIP string) {
	retention := store.EventRetention{MaxAge: 30 * 24 * time.Hour, MaxRows: 50000, MaxBytes: 128 << 20}
	if s.jobs != nil {
		settings := s.jobs.Settings()
		retention = store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes}
	}
	_ = s.store.SaveEvent(store.Event{At: time.Now().UTC(), Type: typ, Level: "info", Message: message, Data: map[string]any{"channelId": channelID, "clientIP": clientIP}}, retention)
}

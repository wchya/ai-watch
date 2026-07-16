package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"ai-watch/internal/store"
)

type requestInputSummary struct {
	PromptBytes         int    `json:"promptBytes,omitempty"`
	PromptSHA256        string `json:"promptSHA256,omitempty"`
	TimeoutSeconds      int    `json:"timeoutSeconds,omitempty"`
	RunOnce             bool   `json:"runOnce"`
	CodexRequestRetries int    `json:"codexRequestRetries,omitempty"`
	CodexStreamRetries  int    `json:"codexStreamRetries,omitempty"`
	ClaudeMaxRetries    int    `json:"claudeMaxRetries,omitempty"`
	FallbackModel       string `json:"fallbackModel,omitempty"`
}

type requestDetailResponse struct {
	RequestID       string              `json:"requestId"`
	JobID           string              `json:"jobId,omitempty"`
	ScheduleID      string              `json:"scheduleId,omitempty"`
	ProviderID      string              `json:"providerId,omitempty"`
	Attempt         int                 `json:"attempt,omitempty"`
	Mode            string              `json:"mode,omitempty"`
	Phase           string              `json:"phase,omitempty"`
	CLI             string              `json:"cli,omitempty"`
	Model           string              `json:"model,omitempty"`
	Provider        string              `json:"provider,omitempty"`
	ConfigSource    string              `json:"configSource,omitempty"`
	TriggerSource   string              `json:"triggerSource,omitempty"`
	ClientIP        string              `json:"clientIP,omitempty"`
	Target          string              `json:"target,omitempty"`
	TargetHost      string              `json:"targetHost,omitempty"`
	TargetPort      string              `json:"targetPort,omitempty"`
	DNSIPs          []string            `json:"dnsIPs,omitempty"`
	DNSError        string              `json:"dnsError,omitempty"`
	ProxyMode       string              `json:"proxyMode,omitempty"`
	ProxyEndpoint   string              `json:"proxyEndpoint,omitempty"`
	CLIExecutable   string              `json:"cliExecutable,omitempty"`
	CLIVersion      string              `json:"cliVersion,omitempty"`
	Status          string              `json:"status"`
	Classification  string              `json:"classification,omitempty"`
	StartedAt       time.Time           `json:"startedAt"`
	EndedAt         *time.Time          `json:"endedAt,omitempty"`
	DurationMillis  *int64              `json:"durationMillis,omitempty"`
	ExitCode        *int64              `json:"exitCode,omitempty"`
	ErrorStage      string              `json:"errorStage,omitempty"`
	ErrorType       string              `json:"errorType,omitempty"`
	Error           string              `json:"error,omitempty"`
	Retryable       *bool               `json:"retryable,omitempty"`
	ResponseExcerpt string              `json:"responseExcerpt,omitempty"`
	NextAttemptAt   string              `json:"nextAttemptAt,omitempty"`
	Input           requestInputSummary `json:"input"`
	Complete        bool                `json:"complete"`
	Recommendation  string              `json:"recommendation"`
}

func (s *Server) requestDetail(w http.ResponseWriter, requestID string) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "request_unavailable", "event store is unavailable")
		return
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > 256 || strings.Contains(requestID, "/") {
		writeError(w, http.StatusBadRequest, "invalid_request_id", "requestId is required and must not exceed 256 bytes")
		return
	}
	events, err := s.store.ListEvents(store.EventFilter{RequestID: requestID, Limit: 10})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_read_failed", err.Error())
		return
	}
	if len(events) == 0 {
		writeError(w, http.StatusNotFound, "request_not_found", "request detail has expired, was cleaned, or does not exist")
		return
	}
	value := requestDetailResponse{RequestID: requestID, Status: "running", Recommendation: "等待请求结束后查看完整结论"}
	var start, end *store.Event
	for index := range events {
		event := &events[index]
		switch event.Type {
		case "request_start":
			start = event
		case "request_end":
			end = event
		}
	}
	if start != nil {
		applyRequestStart(&value, *start)
	}
	if end != nil {
		applyRequestEnd(&value, *end)
	}
	value.Complete = start != nil && end != nil
	value.Recommendation = requestRecommendation(value)
	writeJSON(w, http.StatusOK, value)
}

func applyRequestStart(value *requestDetailResponse, event store.Event) {
	data := event.Data
	value.JobID, value.ScheduleID, value.ProviderID = event.JobID, event.ScheduleID, event.ProviderID
	value.Attempt = mapInt(data, "attempt")
	value.Mode, value.Phase, value.CLI = mapString(data, "mode"), mapString(data, "phase"), mapString(data, "cli")
	value.Model, value.Provider, value.ConfigSource = mapString(data, "model"), mapString(data, "provider"), mapString(data, "configSource")
	value.TriggerSource, value.ClientIP = mapString(data, "triggerSource"), mapString(data, "clientIP")
	value.Target, value.TargetHost, value.TargetPort = mapString(data, "target"), mapString(data, "targetHost"), mapString(data, "targetPort")
	value.ProxyMode, value.ProxyEndpoint = mapString(data, "proxyMode"), mapString(data, "proxyEndpoint")
	value.DNSIPs = mapStrings(data, "dnsIPs")
	value.DNSError = mapString(data, "dnsError")
	value.StartedAt = mapTime(data, "startedAt", event.At)
	if body, ok := data["requestBody"].(map[string]any); ok {
		value.Input = requestInputSummary{PromptBytes: mapInt(body, "promptBytes"), PromptSHA256: mapString(body, "promptSHA256"), TimeoutSeconds: mapInt(body, "timeoutSeconds"), RunOnce: mapBool(body, "runOnce"), CodexRequestRetries: mapInt(body, "codexRequestRetries"), CodexStreamRetries: mapInt(body, "codexStreamRetries"), ClaudeMaxRetries: mapInt(body, "claudeMaxRetries"), FallbackModel: mapString(body, "fallbackModel")}
	}
}

func applyRequestEnd(value *requestDetailResponse, event store.Event) {
	data := event.Data
	if value.JobID == "" {
		value.JobID = event.JobID
	}
	if value.ScheduleID == "" {
		value.ScheduleID = event.ScheduleID
	}
	if value.ProviderID == "" {
		value.ProviderID = event.ProviderID
	}
	if value.Attempt == 0 {
		value.Attempt = mapInt(data, "attempt")
	}
	if value.CLI == "" {
		value.CLI = mapString(data, "cli")
	}
	if value.Model == "" {
		value.Model = mapString(data, "model")
	}
	if value.Phase == "" {
		value.Phase = mapString(data, "phase")
	}
	if value.TriggerSource == "" {
		value.TriggerSource = mapString(data, "triggerSource")
	}
	if value.StartedAt.IsZero() {
		value.StartedAt = mapTime(data, "startedAt", event.At)
	}
	ended := mapTime(data, "endedAt", event.At)
	value.EndedAt = &ended
	value.Status, value.Classification = mapString(data, "status"), mapString(data, "classification")
	value.DurationMillis, value.ExitCode = mapInt64Pointer(data, "durationMillis"), mapInt64Pointer(data, "exitCode")
	value.ErrorStage, value.ErrorType, value.Error = mapString(data, "errorStage"), mapString(data, "errorType"), mapString(data, "error")
	value.Retryable = mapBoolPointer(data, "retryable")
	value.ResponseExcerpt, value.NextAttemptAt = mapString(data, "responseExcerpt"), mapString(data, "nextAttemptAt")
	value.CLIExecutable, value.CLIVersion = mapString(data, "cliExecutable"), mapString(data, "cliVersion")
}

func requestRecommendation(value requestDetailResponse) string {
	switch value.Status {
	case "success":
		return "请求成功，无需重试；可继续观察该 Provider 的可靠性趋势"
	case "running":
		return "请求仍在运行；稍后刷新以获取供应商返回和最终结论"
	case "timeout":
		return "检查网络、代理和上游延迟后重试；持续超时可切换备用 Provider"
	case "start_failed":
		return "检查 CLI 可执行文件、模型参数和运行配置后重新发起请求"
	case "stopped":
		return "请求已主动停止；如仍需验证线路，可重新发起一次测活"
	default:
		if value.Retryable != nil && *value.Retryable {
			return "检查供应商返回摘要后重试；重复失败时测试备用 Provider"
		}
		return "检查凭证、模型、CLI 配置和供应商返回后再执行"
	}
}

func mapString(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
func mapInt(values map[string]any, key string) int {
	if value, ok := values[key].(float64); ok {
		return int(value)
	}
	if value, ok := values[key].(int); ok {
		return value
	}
	return 0
}
func mapBool(values map[string]any, key string) bool { value, _ := values[key].(bool); return value }
func mapBoolPointer(values map[string]any, key string) *bool {
	value, ok := values[key].(bool)
	if !ok {
		return nil
	}
	return &value
}
func mapInt64Pointer(values map[string]any, key string) *int64 {
	value, ok := values[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case float64:
		result := int64(typed)
		return &result
	case int64:
		return &typed
	case int:
		result := int64(typed)
		return &result
	}
	return nil
}
func mapStrings(values map[string]any, key string) []string {
	raw, ok := values[key].([]any)
	if !ok {
		if typed, valid := values[key].([]string); valid {
			return typed
		}
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if value := strings.TrimSpace(fmt.Sprint(item)); value != "" {
			result = append(result, value)
		}
	}
	return result
}
func mapTime(values map[string]any, key string, fallback time.Time) time.Time {
	raw := mapString(values, key)
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC()
	}
	return fallback.UTC()
}

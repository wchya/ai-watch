package api

import (
	"errors"
	"net/http"

	"ai-watch/internal/domain"
	"ai-watch/internal/proxyconfig"
)

func (s *Server) mihomoSubscription(w http.ResponseWriter, r *http.Request) {
	if s.proxy == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy_config_unavailable", "Mihomo subscription configuration is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.proxy.Status(r.Context()))
}

func (s *Server) saveMihomoSubscription(w http.ResponseWriter, r *http.Request) {
	if s.proxy == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy_config_unavailable", "Mihomo subscription configuration is unavailable")
		return
	}
	var input domain.MihomoSubscriptionWrite
	if !decode(w, r, &input) {
		return
	}
	value, err := s.proxy.Apply(r.Context(), input.SubscriptionURL)
	if err != nil {
		proxyConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) clearMihomoSubscription(w http.ResponseWriter, r *http.Request) {
	if s.proxy == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy_config_unavailable", "Mihomo subscription configuration is unavailable")
		return
	}
	value, err := s.proxy.Clear(r.Context())
	if err != nil {
		proxyConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) testMihomoProxy(w http.ResponseWriter, r *http.Request) {
	if s.proxy == nil {
		writeError(w, http.StatusServiceUnavailable, "proxy_config_unavailable", "Mihomo subscription configuration is unavailable")
		return
	}
	value, err := s.proxy.Test(r.Context())
	if err != nil {
		proxyConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func proxyConfigError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, proxyconfig.ErrInvalidSubscriptionURL):
		writeError(w, http.StatusBadRequest, "invalid_subscription_url", "请输入有效的 HTTP 或 HTTPS 订阅地址")
	case errors.Is(err, proxyconfig.ErrEncryptionUnavailable):
		writeError(w, http.StatusServiceUnavailable, "encryption_unavailable", "订阅无法安全加密保存")
	case errors.Is(err, proxyconfig.ErrMihomoUnavailable):
		writeError(w, http.StatusBadGateway, "mihomo_unavailable", "Mihomo 当前不可用")
	case errors.Is(err, proxyconfig.ErrSubscriptionUnavailable):
		writeError(w, http.StatusBadGateway, "subscription_unavailable", "订阅未返回可用代理节点")
	case errors.Is(err, proxyconfig.ErrProxyTestFailed):
		writeError(w, http.StatusBadGateway, "proxy_test_failed", "代理连通测试失败")
	case errors.Is(err, proxyconfig.ErrRollbackFailed):
		writeError(w, http.StatusInternalServerError, "rollback_failed", "新配置应用失败，且旧配置恢复失败")
	default:
		writeError(w, http.StatusInternalServerError, "config_apply_failed", "Mihomo 配置应用失败")
	}
}

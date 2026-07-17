package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/proxyconfig"
)

type apiProxyStore struct {
	value domain.MihomoSubscription
}

func (s *apiProxyStore) LoadMihomoSubscription() (domain.MihomoSubscription, error) {
	return s.value, nil
}
func (s *apiProxyStore) SaveMihomoSubscription(value domain.MihomoSubscription) (domain.MihomoSubscription, error) {
	value.UpdatedAt = time.Date(2026, 7, 16, 9, 30, 0, 0, time.UTC)
	s.value = value
	return value, nil
}
func (s *apiProxyStore) ClearMihomoSubscription() (bool, error) {
	configured := s.value.URL != ""
	s.value = domain.MihomoSubscription{}
	return configured, nil
}

type apiProxyController struct {
	reloads []string
	group   proxyconfig.GroupStatus
}

func (c *apiProxyController) Reload(_ context.Context, path string) error {
	c.reloads = append(c.reloads, path)
	return nil
}
func (c *apiProxyController) GroupStatus(context.Context, string) (proxyconfig.GroupStatus, error) {
	return c.group, nil
}

type apiProxyTester struct{}

func (apiProxyTester) Test(context.Context) error { return nil }

func TestMihomoSubscriptionAPIIsWriteOnlyAndSupportsLifecycle(t *testing.T) {
	proxyStore := &apiProxyStore{}
	controller := &apiProxyController{group: proxyconfig.GroupStatus{NodeCount: 2, CurrentNode: "Hong Kong"}}
	service := proxyconfig.New(proxyStore, controller, apiProxyTester{}, proxyconfig.Options{
		RuntimePath: filepath.Join(t.TempDir(), "runtime.yaml"), RuntimeControllerPath: "/runtime.yaml", BaseControllerPath: "/config.yaml",
		ReadyAttempts: 1, Now: func() time.Time { return time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC) },
	})
	handler := (&Server{}).WithProxyConfig(service).Handler()
	secret := "https://subscription.example/private?token=redis-secret"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/proxy/subscription", strings.NewReader(`{"subscriptionUrl":"`+secret+`"}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "redis-secret") || !strings.Contains(recorder.Body.String(), `"configured":true`) {
		t.Fatalf("save response=%d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/proxy/subscription", nil))
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "redis-secret") || !strings.Contains(recorder.Body.String(), `"nodeCount":2`) {
		t.Fatalf("status response=%d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/proxy/test", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"currentNode":"Hong Kong"`) {
		t.Fatalf("test response=%d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/proxy/subscription", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"configured":false`) || proxyStore.value.URL != "" {
		t.Fatalf("clear response=%d %s stored=%+v", recorder.Code, recorder.Body.String(), proxyStore.value)
	}
	if len(controller.reloads) != 2 || controller.reloads[1] != "/config.yaml" {
		t.Fatalf("reloads=%v", controller.reloads)
	}
}

func TestMihomoSubscriptionAPIRejectsInvalidURL(t *testing.T) {
	service := proxyconfig.New(&apiProxyStore{}, &apiProxyController{}, apiProxyTester{}, proxyconfig.Options{RuntimePath: filepath.Join(t.TempDir(), "runtime.yaml")})
	handler := (&Server{}).WithProxyConfig(service).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/api/proxy/subscription", strings.NewReader(`{"subscriptionUrl":"file:///tmp/config"}`)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_subscription_url"`) {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestProxyConfigErrorMapsRollbackFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	proxyConfigError(recorder, errors.Join(proxyconfig.ErrRollbackFailed, errors.New("secret details")))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "secret details") || !strings.Contains(recorder.Body.String(), `"code":"rollback_failed"`) {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

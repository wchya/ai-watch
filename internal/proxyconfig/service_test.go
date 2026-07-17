package proxyconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/domain"
)

type memoryStore struct {
	value   domain.MihomoSubscription
	saveErr error
}

func (s *memoryStore) LoadMihomoSubscription() (domain.MihomoSubscription, error) {
	return s.value, nil
}
func (s *memoryStore) SaveMihomoSubscription(value domain.MihomoSubscription) (domain.MihomoSubscription, error) {
	if s.saveErr != nil {
		return domain.MihomoSubscription{}, s.saveErr
	}
	value.UpdatedAt = time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	s.value = value
	return value, nil
}
func (s *memoryStore) ClearMihomoSubscription() (bool, error) {
	configured := s.value.URL != ""
	s.value = domain.MihomoSubscription{}
	return configured, nil
}

type fakeController struct {
	reloads []string
	group   GroupStatus
	err     error
}

func (c *fakeController) Reload(_ context.Context, path string) error {
	c.reloads = append(c.reloads, path)
	return c.err
}
func (c *fakeController) GroupStatus(context.Context, string) (GroupStatus, error) {
	return c.group, c.err
}

type fakeTester struct{ err error }

func (t fakeTester) Test(context.Context) error { return t.err }

func testService(t *testing.T, store *memoryStore, controller *fakeController, tester fakeTester) (*Service, string) {
	t.Helper()
	runtimePath := filepath.Join(t.TempDir(), "runtime.yaml")
	service := New(store, controller, tester, Options{
		RuntimePath: runtimePath, RuntimeControllerPath: "/runtime.yaml", BaseControllerPath: "/config.yaml",
		ReadyAttempts: 1, ReadyInterval: time.Millisecond, Now: func() time.Time { return time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC) },
	})
	return service, runtimePath
}

func TestValidateSubscriptionURLAndMask(t *testing.T) {
	for _, value := range []string{"", "ftp://example.com/sub", "https://example.com/sub#token", "relative/path"} {
		if _, err := validateSubscriptionURL(value); !errors.Is(err, ErrInvalidSubscriptionURL) {
			t.Fatalf("value=%q err=%v", value, err)
		}
	}
	value := "https://user:password@example.com/private/sub?token=secret"
	if masked := maskSubscriptionURL(value); masked != "https://example.com/..." || strings.Contains(masked, "secret") || strings.Contains(masked, "password") {
		t.Fatalf("masked=%q", masked)
	}
}

func TestApplyGeneratesControlledConfigAndPersistsAfterValidation(t *testing.T) {
	store := &memoryStore{}
	controller := &fakeController{group: GroupStatus{NodeCount: 3, CurrentNode: "Hong Kong"}}
	service, runtimePath := testService(t, store, controller, fakeTester{})
	status, err := service.Apply(context.Background(), "https://subscription.example/sub?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || !status.Applied || status.NodeCount != 3 || status.CurrentNode != "Hong Kong" || store.value.URL == "" {
		t.Fatalf("status=%+v stored=%+v", status, store.value)
	}
	content, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]any
	if err := json.Unmarshal(content, &configuration); err != nil {
		t.Fatal(err)
	}
	if configuration["rules"].([]any)[0] != "MATCH,PROXY" || len(controller.reloads) != 1 || controller.reloads[0] != "/runtime.yaml" {
		t.Fatalf("configuration=%s reloads=%v", content, controller.reloads)
	}
	providers := configuration["proxy-providers"].(map[string]any)
	subscription := providers["subscription"].(map[string]any)
	path, _ := subscription["path"].(string)
	if path == "./providers/subscription.yaml" || !strings.HasPrefix(path, "./providers/subscription-") {
		t.Fatalf("subscription cache was not versioned: %q", path)
	}
}

func TestApplyRollsBackRuntimeWhenConnectivityFails(t *testing.T) {
	store := &memoryStore{value: domain.MihomoSubscription{URL: "https://old.example/sub", UpdatedAt: time.Now()}}
	controller := &fakeController{group: GroupStatus{NodeCount: 2, CurrentNode: "Old"}}
	service, runtimePath := testService(t, store, controller, fakeTester{err: errors.New("offline")})
	oldConfig := []byte(`{"rules":["MATCH,DIRECT"]}`)
	if err := os.WriteFile(runtimePath, oldConfig, 0600); err != nil {
		t.Fatal(err)
	}
	status, err := service.Apply(context.Background(), "https://new.example/sub")
	if !errors.Is(err, ErrProxyTestFailed) || status.ErrorStage != "connectivity" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	content, readErr := os.ReadFile(runtimePath)
	if readErr != nil || string(content) != string(oldConfig) || store.value.URL != "https://old.example/sub" {
		t.Fatalf("content=%s stored=%+v readErr=%v", content, store.value, readErr)
	}
	if len(controller.reloads) != 2 || controller.reloads[1] != "/runtime.yaml" {
		t.Fatalf("reloads=%v", controller.reloads)
	}
}

func TestApplyWithoutPreviousSubscriptionRollsBackToBase(t *testing.T) {
	store := &memoryStore{}
	controller := &fakeController{group: GroupStatus{NodeCount: 2, CurrentNode: "New"}}
	service, runtimePath := testService(t, store, controller, fakeTester{err: errors.New("offline")})
	if err := os.WriteFile(runtimePath, []byte("stale runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := service.Apply(context.Background(), "https://new.example/sub")
	if !errors.Is(err, ErrProxyTestFailed) {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(runtimePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale runtime was retained: %v", statErr)
	}
	if len(controller.reloads) != 2 || controller.reloads[1] != "/config.yaml" {
		t.Fatalf("reloads=%v", controller.reloads)
	}
}

func TestClearRestoresBaseConfigurationAndDeletesSecret(t *testing.T) {
	store := &memoryStore{value: domain.MihomoSubscription{URL: "https://old.example/sub", UpdatedAt: time.Now()}}
	controller := &fakeController{group: GroupStatus{NodeCount: 1, CurrentNode: "Old"}}
	service, runtimePath := testService(t, store, controller, fakeTester{})
	if err := os.WriteFile(runtimePath, []byte("secret runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := service.Clear(context.Background())
	if err != nil || status.Configured || store.value.URL != "" {
		t.Fatalf("status=%+v stored=%+v err=%v", status, store.value, err)
	}
	if _, err := os.Stat(runtimePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime still exists: %v", err)
	}
	if len(controller.reloads) != 1 || controller.reloads[0] != "/config.yaml" {
		t.Fatalf("reloads=%v", controller.reloads)
	}
}

func TestRestoreFailureRemovesRuntimeAndReturnsToBase(t *testing.T) {
	store := &memoryStore{value: domain.MihomoSubscription{URL: "https://stored.example/sub", UpdatedAt: time.Now()}}
	controller := &fakeController{group: GroupStatus{NodeCount: 2, CurrentNode: "Stored"}}
	service, runtimePath := testService(t, store, controller, fakeTester{err: errors.New("offline")})
	err := service.Restore(context.Background())
	if !errors.Is(err, ErrProxyTestFailed) {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(runtimePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("runtime was not removed: %v", statErr)
	}
	if len(controller.reloads) != 2 || controller.reloads[0] != "/runtime.yaml" || controller.reloads[1] != "/config.yaml" {
		t.Fatalf("reloads=%v", controller.reloads)
	}
	controller.group = GroupStatus{}
	status := service.Status(context.Background())
	if status.ErrorStage != "subscription" || status.Applied {
		t.Fatalf("status=%+v", status)
	}
}

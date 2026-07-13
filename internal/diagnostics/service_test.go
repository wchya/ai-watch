package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/configscan"
	"ai-watch/internal/store"
)

func TestSnapshotReturnsOnlyBoundedDiagnosticMetadata(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	runtimeDir := filepath.Join(root, "runtime")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "jobs", "job-a"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(root, "data"))
	defer st.Close()
	if err := st.SaveEvent(store.Event{At: time.Now(), Type: "job_state"}); err != nil {
		t.Fatal(err)
	}
	scanner := &configscan.Scanner{CodexBin: "codex-test", ClaudeBin: "claude-test"}
	service := New(scanner, nil, st, runtimeDir)
	service.checkCLI = func(_ context.Context, id, _ string) CLIStatus {
		if id == "codex" {
			return CLIStatus{ID: id, Available: true, PathLabel: "codex-test", Version: "codex-cli 0.144.3", CheckState: "ok"}
		}
		return CLIStatus{ID: id, Available: true, PathLabel: "claude-test", Version: "2.1.207 (Claude Code)", CheckState: "ok"}
	}
	snapshot := service.Snapshot(context.Background())
	if snapshot.Status != "ok" || len(snapshot.CLIs) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.CLIs[0].PathLabel != "codex-test" || snapshot.CLIs[0].Version != "codex-cli 0.144.3" {
		t.Fatalf("unexpected Codex metadata: %+v", snapshot.CLIs[0])
	}
	if strings.Contains(snapshot.CLIs[0].PathLabel, root) || snapshot.SQLite.SchemaVersion < 7 || snapshot.SQLite.EventCount != 1 {
		t.Fatalf("diagnostics exposed a path or missed SQLite metadata: %+v", snapshot)
	}
	if snapshot.Runtime.DirectoryEntries != 1 || !snapshot.Runtime.DirectoryReady {
		t.Fatalf("unexpected runtime metadata: %+v", snapshot.Runtime)
	}
}

func TestCLICheckDoesNotReturnSecretsOrUnsafeOutput(t *testing.T) {
	bin := writeVersionCLI(t, t.TempDir(), "unsafe-cli", "sk-abcdefghijklmnop /Users/private/config")
	if got := sanitizeVersion("sk-abcdefghijklmnop /Users/private/config"); got != "" {
		t.Fatalf("unsafe version output escaped: %q", got)
	}
	if got := sanitizeVersion("warning: /private/path\ncodex-cli 0.144.3"); got != "codex-cli 0.144.3" {
		t.Fatalf("safe version line was not recovered: %q", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	result := checkCLI(ctx, "codex", bin)
	if result.CheckState != "timeout" {
		t.Fatalf("canceled version check state=%s", result.CheckState)
	}
}

func writeVersionCLI(t *testing.T, dir, name, version string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\nprintf '%s\\n' '" + strings.ReplaceAll(version, "'", "") + "'\n"
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

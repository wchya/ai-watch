package runner

import (
	"ai-watch/internal/domain"
	"ai-watch/internal/security"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunRedactsAndCleans(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "codex")
	if e := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s READY' \"$OPENAI_API_KEY\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	r := &Runner{CodexBin: bin, RuntimeDir: filepath.Join(root, "run"), MaxOutputBytes: 4096}
	secret := "sk-secret-value"
	cfg := domain.ResolvedConfig{Source: "cc-switch", Provider: "openai", BaseURL: "https://x", APIKey: secret, CodexConfig: "model_provider = \"openai\""}
	o := domain.JobOptions{CLI: domain.CLICodex, Prompt: "hi", Expected: "READY"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, e := r.Run(ctx, "job", o, cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "READY") || strings.Contains(res.Output, secret) {
		t.Fatalf("unexpected result %+v", res)
	}
	if _, e = os.Stat(filepath.Join(root, "run", "jobs", "job")); !os.IsNotExist(e) {
		t.Fatal("temporary directory was not removed")
	}
}
func TestRunTimeout(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "codex")
	_ = os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0700)
	r := &Runner{CodexBin: bin, RuntimeDir: filepath.Join(root, "run"), MaxOutputBytes: 10}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res, e := r.Run(ctx, "job", domain.JobOptions{CLI: domain.CLICodex, Prompt: "x"}, domain.ResolvedConfig{Source: "cc-switch", Provider: "openai", CodexConfig: "model_provider='openai'"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !res.TimedOut {
		t.Fatalf("expected timeout: %+v", res)
	}
}

func TestCodexRunPassesRequestedModel(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "codex")
	argsFile := filepath.Join(root, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nprintf 'READY'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{CodexBin: bin, RuntimeDir: filepath.Join(root, "run"), MaxOutputBytes: 4096}
	res, err := r.Run(context.Background(), "job", domain.JobOptions{
		CLI:    domain.CLICodex,
		Model:  "gpt-requested",
		Prompt: "probe",
	}, domain.ResolvedConfig{
		Source:      "cc-switch",
		Provider:    "openai",
		Model:       "gpt-configured",
		CodexConfig: "model_provider='openai'",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	found := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--model" && args[i+1] == "gpt-requested" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("requested model not passed to Codex: %q", args)
	}
}

func TestCleanupRuntimeJobsRemovesOnlyStaleJobs(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	stale := filepath.Join(runtimeDir, "jobs", "old-job", "auth.json")
	keep := filepath.Join(runtimeDir, "keep.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Runner{RuntimeDir: runtimeDir}
	if err := r.CleanupRuntimeJobs(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(runtimeDir, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("stale jobs remain: %+v", entries)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("non-job runtime data was removed: %v", err)
	}
}

func TestStreamWriterRedactsSecretAcrossWrites(t *testing.T) {
	secret := "sk-super-secret-cross-chunk"
	var streamed strings.Builder
	w := &streamWriter{limit: 4096, secrets: []string{secret}, callback: func(value string) { streamed.WriteString(value) }}
	_, _ = w.Write([]byte("token=" + secret[:9]))
	_, _ = w.Write([]byte(secret[9:] + " READY\nnext=" + secret[:7]))
	_, _ = w.Write([]byte(secret[7:]))
	w.Flush()
	for _, output := range []string{w.String(), streamed.String()} {
		if strings.Contains(output, secret) || strings.Contains(output, secret[:9]) {
			t.Fatalf("secret leaked across chunks: %q", output)
		}
		if !strings.Contains(output, "[REDACTED]") {
			t.Fatalf("redaction marker missing: %q", output)
		}
	}
}

func TestCommandEnvExcludesUnrelatedServiceSecretsAndRedactsAllowedSecrets(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("DINGTALK_WEBHOOK_URL", "https://example.test/robot?access_token=service-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret-value")
	environment := commandEnv(map[string]string{"HOME": "/tmp/job", "ANTHROPIC_AUTH_TOKEN": "provider-secret"})
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "DINGTALK_WEBHOOK_URL") || strings.Contains(joined, "service-secret") {
		t.Fatalf("unrelated service secret reached CLI environment: %s", joined)
	}
	if !strings.Contains(joined, "AWS_SECRET_ACCESS_KEY=aws-secret-value") || !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=provider-secret") {
		t.Fatalf("provider environment was not preserved: %s", joined)
	}
	redacted := security.Redact("aws-secret-value provider-secret", sensitiveEnvValues(environment)...)
	if strings.Contains(redacted, "aws-secret-value") || strings.Contains(redacted, "provider-secret") {
		t.Fatalf("allowed provider secret was not redacted: %s", redacted)
	}
}

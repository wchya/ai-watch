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

func TestCodexRunReadsPromptOnlyFromStdin(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "codex")
	script := "#!/bin/sh\nlast=''\nfor arg in \"$@\"; do last=\"$arg\"; done\n[ \"$last\" = '-' ] || { printf 'missing-stdin-marker:%s' \"$last\"; exit 8; }\nIFS= read -r value || { printf 'missing-stdin'; exit 9; }\n[ \"$value\" = 'probe' ] || { printf 'wrong-stdin:%s' \"$value\"; exit 10; }\nprintf 'READY'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{CodexBin: bin, RuntimeDir: filepath.Join(root, "run"), MaxOutputBytes: 4096}
	res, err := r.Run(context.Background(), "stdin-prompt", domain.JobOptions{CLI: domain.CLICodex, Prompt: "probe"}, domain.ResolvedConfig{Source: "cc-switch", Provider: "openai", CodexConfig: "model_provider='openai'"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "READY") {
		t.Fatalf("codex prompt was not passed via stdin: %+v", res)
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
	environment := commandEnv(map[string]string{"HOME": "/tmp/job", "ANTHROPIC_AUTH_TOKEN": "provider-secret"}, domain.ResolvedConfig{})
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

func TestCommandEnvAppliesDefaultDirectAndCustomProxyPolicies(t *testing.T) {
	for key, value := range map[string]string{
		"HTTP_PROXY": "http://inherited-upper:8000", "HTTPS_PROXY": "http://inherited-upper:8000",
		"ALL_PROXY": "socks5://inherited-upper:1080", "NO_PROXY": "localhost,redis",
		"http_proxy": "http://inherited-lower:8000", "https_proxy": "http://inherited-lower:8000",
		"all_proxy": "socks5://inherited-lower:1080", "no_proxy": "localhost,redis",
	} {
		t.Setenv(key, value)
	}
	t.Setenv("AI_WATCH_DEFAULT_PROXY_URL", "http://mihomo:7890")

	defaultEnv := envSliceMap(commandEnv(nil, domain.ResolvedConfig{ProxyMode: domain.ProxyDefault}))
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if defaultEnv[key] != "http://mihomo:7890" {
			t.Fatalf("default proxy was not applied to %s: %+v", key, defaultEnv)
		}
	}
	if _, ok := defaultEnv["ALL_PROXY"]; ok {
		t.Fatalf("default HTTP proxy retained a conflicting ALL_PROXY: %+v", defaultEnv)
	}
	if defaultEnv["NO_PROXY"] != "localhost,redis" {
		t.Fatalf("default proxy should preserve NO_PROXY: %+v", defaultEnv)
	}

	directEnv := envSliceMap(commandEnv(nil, domain.ResolvedConfig{ProxyMode: domain.ProxyDirect}))
	for _, key := range routeProxyEnvKeys {
		if _, ok := directEnv[key]; ok {
			t.Fatalf("direct mode retained %s: %+v", key, directEnv)
		}
	}
	if directEnv["NO_PROXY"] != "localhost,redis" || directEnv["no_proxy"] != "localhost,redis" {
		t.Fatalf("direct mode should preserve no-proxy exclusions: %+v", directEnv)
	}

	customHTTP := envSliceMap(commandEnv(nil, domain.ResolvedConfig{ProxyMode: domain.ProxyCustom, ProxyURL: "http://custom.example:8080"}))
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if customHTTP[key] != "http://custom.example:8080" {
			t.Fatalf("custom HTTP proxy was not applied to %s: %+v", key, customHTTP)
		}
	}
	if _, ok := customHTTP["ALL_PROXY"]; ok {
		t.Fatalf("custom HTTP proxy retained inherited ALL_PROXY: %+v", customHTTP)
	}
	if _, ok := customHTTP["all_proxy"]; ok {
		t.Fatalf("custom HTTP proxy retained inherited all_proxy: %+v", customHTTP)
	}

	customSOCKS := envSliceMap(commandEnv(nil, domain.ResolvedConfig{ProxyMode: domain.ProxyCustom, ProxyURL: "socks5h://custom.example:1080"}))
	for _, key := range routeProxyEnvKeys {
		if customSOCKS[key] != "socks5h://custom.example:1080" {
			t.Fatalf("custom SOCKS proxy was not applied to %s: %+v", key, customSOCKS)
		}
	}
}

func TestRunRedactsCredentialedCustomProxyURL(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s proxy-password READY' \"$HTTPS_PROXY\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{CodexBin: bin, RuntimeDir: filepath.Join(root, "run"), MaxOutputBytes: 4096}
	proxyURL := "http://proxy-user:proxy-password@proxy.example:8080"
	res, err := r.Run(context.Background(), "proxy-redaction", domain.JobOptions{
		CLI: domain.CLICodex, Prompt: "probe",
	}, domain.ResolvedConfig{
		Source: "manual", Provider: "openai", CodexConfig: "model_provider='openai'",
		ProxyMode: domain.ProxyCustom, ProxyURL: proxyURL,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "READY") || strings.Contains(res.Output, proxyURL) || strings.Contains(res.Output, "proxy-password") || !strings.Contains(res.Output, "[REDACTED]") {
		t.Fatalf("credentialed proxy URL was not redacted: %+v", res)
	}
}

func envSliceMap(environment []string) map[string]string {
	result := make(map[string]string, len(environment))
	for _, entry := range environment {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

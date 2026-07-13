package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/security"
)

type Result struct {
	ExitCode int
	Output   string
	TimedOut bool
	Stopped  bool
}

type Runner struct {
	CodexBin, ClaudeBin, RuntimeDir string
	MaxOutputBytes                  int
}

func New() *Runner {
	return &Runner{CodexBin: env("CODEX_BIN", "codex"), ClaudeBin: env("CLAUDE_BIN", "claude"), RuntimeDir: env("AI_WATCH_RUNTIME_DIR", "/run/ai-watch"), MaxOutputBytes: 256 << 10}
}
func env(k, v string) string {
	if x := os.Getenv(k); x != "" {
		return x
	}
	return v
}

func (r *Runner) CleanupRuntimeJobs() error {
	root := filepath.Join(r.RuntimeDir, "jobs")
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return os.MkdirAll(root, 0700)
}

func (r *Runner) Run(ctx context.Context, jobID string, opts domain.JobOptions, cfg domain.ResolvedConfig, output func(string)) (Result, error) {
	temp, err := r.prepare(jobID, opts.CLI, cfg)
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(temp)
	var cmd *exec.Cmd
	if opts.CLI == domain.CLICodex {
		provider := cfg.Provider
		if provider == "" {
			provider = "openai"
		}
		args := []string{"exec", "-c", fmt.Sprintf("model_providers.%s.request_max_retries=%d", provider, opts.CodexRequestRetries), "-c", fmt.Sprintf("model_providers.%s.stream_max_retries=%d", provider, opts.CodexStreamRetries), "--disable", "hooks", "--ephemeral", "--ignore-rules", "--skip-git-repo-check", "-s", "read-only"}
		if model := firstNonEmpty(opts.Model, cfg.Model); model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, opts.Prompt)
		cmd = exec.Command(r.CodexBin, args...)
		cmd.Env = replaceEnv(os.Environ(), map[string]string{"CODEX_HOME": temp, "OPENAI_API_KEY": cfg.APIKey})
	} else if opts.CLI == domain.CLIClaude {
		args := []string{"--print", "--output-format", "text", "--no-session-persistence", "--safe-mode", "--permission-mode", "dontAsk", "--name", opts.SessionName, "--tools", ""}
		if opts.Model != "" {
			args = append(args, "--model", opts.Model)
		} else if cfg.Model != "" {
			args = append(args, "--model", cfg.Model)
		}
		if opts.FallbackModel != "" {
			args = append(args, "--fallback-model", opts.FallbackModel)
		}
		cmd = exec.Command(r.ClaudeBin, args...)
		vars := map[string]string{"CLAUDE_CONFIG_DIR": temp, "CLAUDE_CODE_MAX_RETRIES": fmt.Sprint(opts.ClaudeMaxRetries)}
		for k, v := range cfg.ClaudeEnv {
			vars[k] = v
		}
		if cfg.BaseURL != "" {
			vars["ANTHROPIC_BASE_URL"] = cfg.BaseURL
		}
		if cfg.APIKey != "" {
			vars["ANTHROPIC_AUTH_TOKEN"] = cfg.APIKey
			vars["ANTHROPIC_API_KEY"] = cfg.APIKey
		}
		cmd.Env = replaceEnv(os.Environ(), vars)
		cmd.Stdin = strings.NewReader(opts.Prompt + "\n")
	} else {
		return Result{}, errors.New("unsupported cli")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	collector := &streamWriter{limit: r.MaxOutputBytes, secret: cfg.APIKey, callback: output}
	cmd.Stdout = collector
	cmd.Stderr = collector
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		collector.Flush()
		return Result{ExitCode: exitCode(err), Output: collector.String()}, nil
	case <-ctx.Done():
		terminateGroup(cmd.Process.Pid)
		select {
		case <-done:
		case <-time.After(750 * time.Millisecond):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
		collector.Flush()
		return Result{ExitCode: 124, Output: collector.String(), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Stopped: errors.Is(ctx.Err(), context.Canceled)}, nil
	}
}

func terminateGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func (r *Runner) prepare(jobID string, cli domain.CLI, cfg domain.ResolvedConfig) (string, error) {
	root := filepath.Join(r.RuntimeDir, "jobs")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	dir := filepath.Join(root, jobID)
	prepared := false
	defer func() {
		if !prepared {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	if cfg.Source == "current" && cfg.ConfigDir != "" {
		if cli == domain.CLIClaude {
			for _, name := range []string{"settings.json", ".credentials.json"} {
				if b, e := os.ReadFile(filepath.Join(cfg.ConfigDir, name)); e == nil {
					if e = os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
						return "", e
					}
				}
			}
		} else {
			for _, name := range []string{"config.toml", "auth.json"} {
				if b, e := os.ReadFile(filepath.Join(cfg.ConfigDir, name)); e == nil {
					if e = os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
						return "", e
					}
				}
			}
		}
	}
	if cli == domain.CLICodex {
		config := cfg.CodexConfig
		if config == "" {
			b, e := os.ReadFile(filepath.Join(cfg.ConfigDir, "config.toml"))
			if e != nil {
				return "", e
			}
			config = string(b)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0600); err != nil {
			return "", err
		}
		if cfg.Source != "current" {
			auth, _ := json.Marshal(map[string]string{"OPENAI_API_KEY": cfg.APIKey})
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), auth, 0600); err != nil {
				return "", err
			}
		} else if len(cfg.AuthJSON) > 0 {
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), cfg.AuthJSON, 0600); err != nil {
				return "", err
			}
		}
	}
	prepared = true
	return dir, nil
}

func replaceEnv(base []string, values map[string]string) []string {
	m := map[string]string{}
	for _, v := range base {
		if p := strings.SplitN(v, "=", 2); len(p) == 2 {
			m[p[0]] = p[1]
		}
	}
	for k, v := range values {
		if v != "" {
			m[k] = v
		}
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type streamWriter struct {
	mu       sync.Mutex
	buf      []byte
	limit    int
	secret   string
	callback func(string)
	pending  []byte
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.pending = append(w.pending, p...)
	lastNewline := strings.LastIndexByte(string(w.pending), '\n')
	var clean string
	if lastNewline >= 0 {
		clean = security.Redact(string(w.pending[:lastNewline+1]), w.secret)
		w.pending = append([]byte(nil), w.pending[lastNewline+1:]...)
		w.appendCleanLocked(clean)
	} else if len(w.pending) > w.limit {
		w.pending = append([]byte(nil), w.pending[len(w.pending)-w.limit:]...)
	}
	w.mu.Unlock()
	if clean != "" && w.callback != nil {
		w.callback(clean)
	}
	return len(p), nil
}

func (w *streamWriter) Flush() {
	w.mu.Lock()
	clean := security.Redact(string(w.pending), w.secret)
	w.pending = nil
	w.appendCleanLocked(clean)
	w.mu.Unlock()
	if clean != "" && w.callback != nil {
		w.callback(clean)
	}
}

func (w *streamWriter) appendCleanLocked(clean string) {
	w.buf = append(w.buf, clean...)
	if len(w.buf) > w.limit {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.limit:]...)
	}
}
func (w *streamWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(append([]byte(nil), w.buf...))
}

var _ io.Writer = (*streamWriter)(nil)

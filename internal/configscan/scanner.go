package configscan

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"ai-watch/internal/domain"
	"ai-watch/internal/security"
)

type Scanner struct {
	CodexDir   string
	ClaudeDir  string
	CCSwitchDB string
	CodexBin   string
	ClaudeBin  string
	SQLiteBin  string
}

func New() *Scanner {
	home, _ := os.UserHomeDir()
	return &Scanner{
		CodexDir:   value("CODEX_CONFIG_DIR", value("CODEX_HOME", filepath.Join(home, ".codex"))),
		ClaudeDir:  value("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")),
		CCSwitchDB: value("CCSWITCH_DB", filepath.Join(home, ".cc-switch", "cc-switch.db")),
		CodexBin:   value("CODEX_BIN", "codex"), ClaudeBin: value("CLAUDE_BIN", "claude"), SQLiteBin: value("SQLITE_BIN", "sqlite3"),
	}
}

func value(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func exists(path string) bool   { _, err := os.Stat(path); return err == nil }
func available(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func (s *Scanner) Status() domain.ConfigStatus {
	return domain.ConfigStatus{
		CodexCLI: available(s.CodexBin), ClaudeCLI: available(s.ClaudeBin), SQLiteCLI: available(s.SQLiteBin),
		CodexConfig: exists(filepath.Join(s.CodexDir, "config.toml")), ClaudeConfig: exists(filepath.Join(s.ClaudeDir, "settings.json")), CCSwitchDB: exists(s.CCSwitchDB),
		CodexPath: s.CodexDir, ClaudePath: s.ClaudeDir, CCSwitchPath: s.CCSwitchDB,
	}
}

func (s *Scanner) Providers(cli domain.CLI) ([]domain.Provider, error) {
	if cli == "" {
		a, e := s.Providers(domain.CLICodex)
		if e != nil {
			return nil, e
		}
		b, e := s.Providers(domain.CLIClaude)
		return append(a, b...), e
	}
	if cli != domain.CLICodex && cli != domain.CLIClaude {
		return nil, errors.New("cli must be codex or claude")
	}
	providers := []domain.Provider{}
	if (cli == domain.CLICodex && exists(filepath.Join(s.CodexDir, "config.toml"))) || (cli == domain.CLIClaude && exists(filepath.Join(s.ClaudeDir, "settings.json"))) {
		providers = append(providers, domain.Provider{ID: "", Name: "当前 CLI 配置", CLI: cli, Current: true})
	}
	if !exists(s.CCSwitchDB) {
		return providers, nil
	}
	if !available(s.SQLiteBin) {
		return providers, nil
	}
	q := fmt.Sprintf(`SELECT id, name, is_current, settings_config FROM providers WHERE app_type='%s' ORDER BY COALESCE(sort_index,999999), created_at, id;`, sqlQuote(string(cli)))
	cmd := exec.Command(s.SQLiteBin, "-readonly", "-json", s.CCSwitchDB, q)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("query cc switch: %w", err)
	}
	var rows []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Current  int    `json:"is_current"`
		Settings string `json:"settings_config"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("decode cc switch rows: %w", err)
	}
	for _, row := range rows {
		var raw struct {
			Config string            `json:"config"`
			Auth   map[string]string `json:"auth"`
			Env    map[string]string `json:"env"`
		}
		_ = json.Unmarshal([]byte(row.Settings), &raw)
		p := domain.Provider{ID: row.ID, Name: row.Name, CLI: cli, Current: row.Current == 1, Model: raw.Env["ANTHROPIC_MODEL"]}
		if cli == domain.CLICodex {
			c := parseCodex(raw.Config)
			p.BaseURL = c.BaseURL
			p.Model = c.Model
			p.MaskedKey = security.Mask(raw.Auth["OPENAI_API_KEY"])
		} else {
			p.BaseURL = raw.Env["ANTHROPIC_BASE_URL"]
			p.MaskedKey = security.Mask(first(raw.Env["ANTHROPIC_AUTH_TOKEN"], raw.Env["ANTHROPIC_API_KEY"], raw.Env["OPENROUTER_API_KEY"], raw.Env["GOOGLE_API_KEY"]))
		}
		providers = append(providers, p)
	}
	return providers, nil
}

func sqlQuote(v string) string { return strings.ReplaceAll(v, "'", "''") }
func first(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

func (s *Scanner) Resolve(cli domain.CLI, providerID string) (domain.ResolvedConfig, error) {
	if providerID == "" || providerID == "current" {
		if cli == domain.CLICodex {
			return s.currentCodex()
		}
		if cli == domain.CLIClaude {
			return s.currentClaude()
		}
		return domain.ResolvedConfig{}, errors.New("unsupported cli")
	}
	return s.ccProvider(cli, providerID)
}

func (s *Scanner) ccProvider(cli domain.CLI, id string) (domain.ResolvedConfig, error) {
	q := fmt.Sprintf(`SELECT name, settings_config FROM providers WHERE app_type='%s' AND id='%s' LIMIT 1;`, sqlQuote(string(cli)), sqlQuote(id))
	out, err := exec.Command(s.SQLiteBin, "-readonly", "-json", s.CCSwitchDB, q).Output()
	if err != nil {
		return domain.ResolvedConfig{}, err
	}
	var rows []struct {
		Name     string `json:"name"`
		Settings string `json:"settings_config"`
	}
	if json.Unmarshal(out, &rows) != nil || len(rows) != 1 {
		return domain.ResolvedConfig{}, errors.New("provider not found")
	}
	var raw struct {
		Config string            `json:"config"`
		Auth   map[string]string `json:"auth"`
		Env    map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(rows[0].Settings), &raw); err != nil {
		return domain.ResolvedConfig{}, fmt.Errorf("invalid provider settings: %w", err)
	}
	r := domain.ResolvedConfig{Source: "cc-switch", ProviderID: id, ProviderName: rows[0].Name, ClaudeEnv: raw.Env}
	if cli == domain.CLICodex {
		c := parseCodex(raw.Config)
		r.Provider = c.Provider
		r.Model = c.Model
		r.BaseURL = c.BaseURL
		r.APIKey = raw.Auth["OPENAI_API_KEY"]
		r.APIKeySource = "CC Switch auth.OPENAI_API_KEY"
		r.CodexConfig = raw.Config
		if r.CodexConfig == "" || r.APIKey == "" {
			return domain.ResolvedConfig{}, errors.New("Codex provider requires config and OPENAI_API_KEY")
		}
	} else {
		r.Provider = "anthropic-compatible"
		r.BaseURL = raw.Env["ANTHROPIC_BASE_URL"]
		r.Model = raw.Env["ANTHROPIC_MODEL"]
		r.APIKey = first(raw.Env["ANTHROPIC_AUTH_TOKEN"], raw.Env["ANTHROPIC_API_KEY"], raw.Env["OPENROUTER_API_KEY"], raw.Env["GOOGLE_API_KEY"])
		for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY", "GOOGLE_API_KEY"} {
			if raw.Env[k] != "" {
				r.APIKeySource = "CC Switch env." + k
				break
			}
		}
		if r.BaseURL == "" || r.APIKey == "" {
			return domain.ResolvedConfig{}, errors.New("Claude provider requires ANTHROPIC_BASE_URL and API key")
		}
	}
	return r, nil
}

type codexTOML struct{ Provider, Model, BaseURL, APIKey, APIKeyEnv string }

func parseCodex(text string) codexTOML {
	var c codexTOML
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		p := strings.SplitN(line, "=", 2)
		if len(p) != 2 {
			continue
		}
		k, v := strings.TrimSpace(p[0]), strings.Trim(strings.TrimSpace(p[1]), "\"'")
		if section == "" {
			if k == "model_provider" {
				c.Provider = v
			}
			if k == "model" {
				c.Model = v
			}
		}
		if section == "model_providers."+c.Provider {
			switch k {
			case "base_url":
				c.BaseURL = v
			case "api_key":
				c.APIKey = v
			case "api_key_env_var":
				c.APIKeyEnv = v
			}
		}
	}
	if c.Provider == "" {
		c.Provider = "openai"
	}
	if c.BaseURL == "" {
		c.BaseURL = first(os.Getenv("OPENAI_BASE_URL"), os.Getenv("CODEX_BASE_URL"), "https://api.openai.com/v1")
	}
	return c
}

func (s *Scanner) currentCodex() (domain.ResolvedConfig, error) {
	b, err := os.ReadFile(filepath.Join(s.CodexDir, "config.toml"))
	if err != nil {
		return domain.ResolvedConfig{}, fmt.Errorf("read Codex config: %w", err)
	}
	c := parseCodex(string(b))
	key := c.APIKey
	if key == "" && c.APIKeyEnv != "" {
		key = os.Getenv(c.APIKeyEnv)
	}
	if key == "" {
		key = first(os.Getenv(strings.ToUpper(strings.ReplaceAll(c.Provider, "-", "_"))+"_API_KEY"), os.Getenv("OPENAI_API_KEY"), os.Getenv("CODEX_API_KEY"))
	}
	authBytes, _ := os.ReadFile(filepath.Join(s.CodexDir, "auth.json"))
	if key == "" {
		var auth map[string]any
		if json.Unmarshal(authBytes, &auth) == nil {
			key, _ = auth["OPENAI_API_KEY"].(string)
		}
	}
	identity := key
	if identity == "" {
		sum := sha256.Sum256(authBytes)
		identity = fmt.Sprintf("auth:%x", sum[:])
	}
	return domain.ResolvedConfig{Source: "current", Provider: c.Provider, Model: c.Model, BaseURL: c.BaseURL, APIKey: key, AuthJSON: authBytes, LockIdentity: identity, APIKeySource: "Codex config/environment", CodexConfig: string(b), ConfigDir: s.CodexDir}, nil
}

func (s *Scanner) currentClaude() (domain.ResolvedConfig, error) {
	env := map[string]string{}
	p := filepath.Join(s.ClaudeDir, "settings.json")
	if b, err := os.ReadFile(p); err == nil {
		var raw struct {
			Env map[string]any `json:"env"`
		}
		if json.Unmarshal(b, &raw) == nil {
			for k, v := range raw.Env {
				env[k] = fmt.Sprint(v)
			}
		}
	}
	get := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return env[k]
	}
	base := first(os.Getenv("CLAUDE_BASE_URL"), get("ANTHROPIC_BASE_URL"), "https://api.anthropic.com")
	key := first(get("ANTHROPIC_API_KEY"), get("ANTHROPIC_AUTH_TOKEN"), get("CLAUDE_API_KEY"))
	provider := "anthropic"
	if base != "https://api.anthropic.com" {
		provider = "anthropic-compatible"
	}
	if enabled(get("CLAUDE_CODE_USE_BEDROCK")) {
		provider = "bedrock"
	}
	if enabled(get("CLAUDE_CODE_USE_VERTEX")) {
		provider = "vertex"
	}
	identity := key
	if identity == "" {
		if b, e := os.ReadFile(p); e == nil {
			sum := sha256.Sum256(b)
			identity = fmt.Sprintf("settings:%x", sum[:])
		}
	}
	return domain.ResolvedConfig{Source: "current", Provider: provider, Model: get("ANTHROPIC_MODEL"), BaseURL: base, APIKey: key, LockIdentity: identity, APIKeySource: "Claude config/environment", ClaudeEnv: env, ConfigDir: s.ClaudeDir}, nil
}

func enabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func ParsePositive(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, errors.New("must be non-negative")
	}
	return n, nil
}

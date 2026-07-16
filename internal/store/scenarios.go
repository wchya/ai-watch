package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
)

const maxTestScenarios = 100

var scenarioID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

func defaultTestScenarios() []domain.TestScenario {
	return []domain.TestScenario{
		{ID: "basic-ready", Name: "基础可用性", Description: "验证 Provider 能完成最小文本响应。", Enabled: true, Prompt: "hi，只回复 READY", AssertionType: "contains", Expected: "READY", TimeoutSeconds: 15, BuiltIn: true},
		{ID: "json-object", Name: "JSON 格式遵循", Description: "验证模型能返回合法 JSON Object。", Enabled: true, Prompt: `只返回一个 JSON 对象，必须包含字段 "status"，值为 "READY"。不要输出 Markdown。`, AssertionType: "json", TimeoutSeconds: 20, BuiltIn: true},
	}
}

func normalizeTestScenario(value domain.TestScenario) (domain.TestScenario, error) {
	value.ID = strings.ToLower(strings.TrimSpace(value.ID))
	value.Name = strings.TrimSpace(value.Name)
	value.Description = strings.TrimSpace(value.Description)
	value.Prompt = strings.TrimSpace(value.Prompt)
	value.AssertionType = strings.ToLower(strings.TrimSpace(value.AssertionType))
	value.Expected = strings.TrimSpace(value.Expected)
	if value.ID == "" {
		value.ID = "scenario-" + randomHex(8)
	}
	if !scenarioID.MatchString(value.ID) {
		return domain.TestScenario{}, errors.New("scenario id must use lowercase letters, numbers, dot, underscore, or hyphen")
	}
	if value.Name == "" || len(value.Name) > 160 {
		return domain.TestScenario{}, errors.New("scenario name is required and must not exceed 160 bytes")
	}
	if value.CLI != "" && value.CLI != domain.CLICodex && value.CLI != domain.CLIClaude {
		return domain.TestScenario{}, errors.New("scenario cli must be empty, codex, or claude")
	}
	if value.Prompt == "" || len(value.Prompt) > 16<<10 {
		return domain.TestScenario{}, errors.New("scenario prompt is required and must not exceed 16 KiB")
	}
	if value.Description != "" && len(value.Description) > 2<<10 {
		return domain.TestScenario{}, errors.New("scenario description must not exceed 2 KiB")
	}
	switch value.AssertionType {
	case "contains", "exact", "regex":
		if value.Expected == "" || len(value.Expected) > 4<<10 {
			return domain.TestScenario{}, errors.New("scenario expected value is required and must not exceed 4 KiB")
		}
		if value.AssertionType == "regex" {
			if _, err := regexp.Compile(value.Expected); err != nil {
				return domain.TestScenario{}, fmt.Errorf("invalid scenario regular expression: %w", err)
			}
		}
	case "json":
		value.Expected = ""
	default:
		return domain.TestScenario{}, errors.New("scenario assertionType must be contains, exact, regex, or json")
	}
	if value.TimeoutSeconds < 0 || value.TimeoutSeconds > 3600 {
		return domain.TestScenario{}, errors.New("scenario timeoutSeconds must be 0..3600")
	}
	return value, nil
}

func sortTestScenarios(values []domain.TestScenario) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].BuiltIn != values[j].BuiltIn {
			return values[i].BuiltIn
		}
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].ID < values[j].ID
	})
}

func (s *JSON) applyTestScenariosV12() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin test scenario migration: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS test_scenarios (id TEXT PRIMARY KEY, body BLOB NOT NULL, updated_at_ns INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create test scenarios: %w", err)
	}
	if _, err = tx.Exec(`ALTER TABLE schedules ADD COLUMN scenario_id TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return fmt.Errorf("add schedule scenario id: %w", err)
	}
	now := time.Now().UTC()
	for index, value := range defaultTestScenarios() {
		value.CreatedAt, value.UpdatedAt = now, now
		if err = upsertScenarioSQL(tx, value, true, now.Add(time.Duration(index)*time.Nanosecond)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(12, ?)`, now.UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *JSON) ListTestScenarios() ([]domain.TestScenario, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT body FROM test_scenarios`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []domain.TestScenario
	for rows.Next() {
		var body []byte
		var value domain.TestScenario
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	sortTestScenarios(values)
	return values, rows.Err()
}

func (s *JSON) GetTestScenario(id string) (domain.TestScenario, error) {
	if err := s.ready(); err != nil {
		return domain.TestScenario{}, err
	}
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM test_scenarios WHERE id = ?`, strings.TrimSpace(id)).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.TestScenario{}, fs.ErrNotExist
		}
		return domain.TestScenario{}, err
	}
	var value domain.TestScenario
	return value, json.Unmarshal(body, &value)
}

func (s *JSON) UpsertTestScenario(value domain.TestScenario) (domain.TestScenario, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return domain.TestScenario{}, err
	}
	var err error
	if value, err = normalizeTestScenario(value); err != nil {
		return domain.TestScenario{}, err
	}
	now := time.Now().UTC()
	if old, getErr := s.getTestScenarioUnlocked(value.ID); getErr == nil {
		value.CreatedAt, value.BuiltIn = old.CreatedAt, old.BuiltIn
	} else {
		var count int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM test_scenarios`).Scan(&count); err != nil {
			return domain.TestScenario{}, err
		}
		if count >= maxTestScenarios {
			return domain.TestScenario{}, errors.New("test scenario limit reached")
		}
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	if err = upsertScenarioSQL(s.db, value, false, now); err != nil {
		return domain.TestScenario{}, err
	}
	return value, nil
}

func (s *JSON) getTestScenarioUnlocked(id string) (domain.TestScenario, error) {
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM test_scenarios WHERE id = ?`, id).Scan(&body); err != nil {
		return domain.TestScenario{}, err
	}
	var value domain.TestScenario
	return value, json.Unmarshal(body, &value)
}

func (s *JSON) DeleteTestScenario(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.getTestScenarioUnlocked(strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if value.BuiltIn {
		return false, errors.New("built-in test scenario cannot be deleted")
	}
	result, err := s.db.Exec(`DELETE FROM test_scenarios WHERE id = ?`, value.ID)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count > 0, nil
}

func upsertScenarioSQL(exec sqlExecer, value domain.TestScenario, seed bool, updated time.Time) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	query := `INSERT INTO test_scenarios(id, body, updated_at_ns) VALUES(?, ?, ?) ON CONFLICT(id) DO UPDATE SET body=excluded.body, updated_at_ns=excluded.updated_at_ns`
	if seed {
		query = `INSERT OR IGNORE INTO test_scenarios(id, body, updated_at_ns) VALUES(?, ?, ?)`
	}
	_, err = exec.Exec(query, value.ID, body, updated.UnixNano())
	return err
}

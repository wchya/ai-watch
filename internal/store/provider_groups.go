package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
)

const maxProviderGroups = 50

func normalizeProviderGroup(value domain.ProviderGroup) (domain.ProviderGroup, error) {
	value.ID = strings.ToLower(strings.TrimSpace(value.ID))
	value.Name = strings.TrimSpace(value.Name)
	value.PrimaryProviderID = strings.TrimSpace(value.PrimaryProviderID)
	value.ActiveProviderID = strings.TrimSpace(value.ActiveProviderID)
	value.Mode = strings.ToLower(strings.TrimSpace(value.Mode))
	value.ScenarioID = strings.TrimSpace(value.ScenarioID)
	if value.ID == "" {
		value.ID = "group-" + randomHex(8)
	}
	if !scenarioID.MatchString(value.ID) {
		return domain.ProviderGroup{}, errors.New("provider group id must use lowercase letters, numbers, dot, underscore, or hyphen")
	}
	if value.Name == "" || len(value.Name) > 160 {
		return domain.ProviderGroup{}, errors.New("provider group name is required and must not exceed 160 bytes")
	}
	if value.CLI != domain.CLICodex && value.CLI != domain.CLIClaude {
		return domain.ProviderGroup{}, errors.New("provider group cli must be codex or claude")
	}
	if value.PrimaryProviderID == "" {
		return domain.ProviderGroup{}, errors.New("primaryProviderId is required")
	}
	if len(value.BackupProviderIDs) < 1 || len(value.BackupProviderIDs) > 19 {
		return domain.ProviderGroup{}, errors.New("provider group must contain 1..19 backup providers")
	}
	seen := map[string]bool{}
	seen[value.PrimaryProviderID] = true
	for index := range value.BackupProviderIDs {
		value.BackupProviderIDs[index] = strings.TrimSpace(value.BackupProviderIDs[index])
		if value.BackupProviderIDs[index] == "" || seen[value.BackupProviderIDs[index]] {
			return domain.ProviderGroup{}, errors.New("provider group members must be unique")
		}
		seen[value.BackupProviderIDs[index]] = true
	}
	if value.ScenarioID == "" {
		return domain.ProviderGroup{}, errors.New("scenarioId is required")
	}
	if value.Mode == "" {
		value.Mode = "advisory"
	}
	if value.Mode != "advisory" && value.Mode != "automatic" {
		return domain.ProviderGroup{}, errors.New("provider group mode must be advisory or automatic")
	}
	if value.ActiveProviderID == "" {
		value.ActiveProviderID = value.PrimaryProviderID
	}
	if !seen[value.ActiveProviderID] {
		return domain.ProviderGroup{}, errors.New("activeProviderId must reference the primary or a backup provider")
	}
	if value.RecoveryThreshold == 0 {
		value.RecoveryThreshold = 2
	}
	if value.RecoveryThreshold < 1 || value.RecoveryThreshold > 100 {
		return domain.ProviderGroup{}, errors.New("provider group recoveryThreshold must be 1..100")
	}
	if value.RecoveryProbeIntervalSeconds == 0 {
		value.RecoveryProbeIntervalSeconds = 300
	}
	if value.RecoveryProbeIntervalSeconds < 30 || value.RecoveryProbeIntervalSeconds > 86400 {
		return domain.ProviderGroup{}, errors.New("provider group recoveryProbeIntervalSeconds must be 30..86400")
	}
	if value.FailureThreshold == 0 {
		value.FailureThreshold = 3
	}
	if value.CooldownSeconds == 0 {
		value.CooldownSeconds = 300
	}
	if value.FailureThreshold < 1 || value.FailureThreshold > 100 || value.CooldownSeconds < 0 || value.CooldownSeconds > 86400 {
		return domain.ProviderGroup{}, errors.New("provider group failureThreshold must be 1..100 and cooldownSeconds 0..86400")
	}
	if value.MaintenanceStartsAt != nil && value.MaintenanceUntil == nil {
		return domain.ProviderGroup{}, errors.New("maintenanceUntil is required when maintenanceStartsAt is set")
	}
	if value.MaintenanceStartsAt != nil && value.MaintenanceUntil != nil && !value.MaintenanceUntil.After(*value.MaintenanceStartsAt) {
		return domain.ProviderGroup{}, errors.New("maintenanceUntil must be after maintenanceStartsAt")
	}
	if value.SLOTargetPercent == 0 {
		value.SLOTargetPercent = 99.9
	}
	if value.SLOWindow == "" {
		value.SLOWindow = "7d"
	}
	if value.SLOMinimumSamples == 0 {
		value.SLOMinimumSamples = 20
	}
	if value.SLOTargetPercent < 90 || value.SLOTargetPercent > 99.999 {
		return domain.ProviderGroup{}, errors.New("provider group sloTargetPercent must be 90..99.999")
	}
	if value.SLOWindow != "24h" && value.SLOWindow != "7d" && value.SLOWindow != "30d" {
		return domain.ProviderGroup{}, errors.New("provider group sloWindow must be 24h, 7d, or 30d")
	}
	if value.SLOMinimumSamples < 1 || value.SLOMinimumSamples > 100000 {
		return domain.ProviderGroup{}, errors.New("provider group sloMinimumSamples must be 1..100000")
	}
	return value, nil
}

func sortProviderGroups(values []domain.ProviderGroup) {
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
}

func (s *JSON) applyProviderGroupsV13() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS provider_groups (id TEXT PRIMARY KEY, body BLOB NOT NULL, updated_at_ns INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create provider groups: %w", err)
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(13, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *JSON) applyProviderGroupSchedulesV15() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE schedules ADD COLUMN provider_group_id TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return fmt.Errorf("add schedule provider group id: %w", err)
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(15, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *JSON) ListProviderGroups() ([]domain.ProviderGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT body FROM provider_groups`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []domain.ProviderGroup
	for rows.Next() {
		var body []byte
		var value domain.ProviderGroup
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	sortProviderGroups(values)
	return values, rows.Err()
}

func (s *JSON) GetProviderGroup(id string) (domain.ProviderGroup, error) {
	if err := s.ready(); err != nil {
		return domain.ProviderGroup{}, err
	}
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM provider_groups WHERE id = ?`, strings.TrimSpace(id)).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ProviderGroup{}, fs.ErrNotExist
		}
		return domain.ProviderGroup{}, err
	}
	var value domain.ProviderGroup
	return value, json.Unmarshal(body, &value)
}

func (s *JSON) UpsertProviderGroup(value domain.ProviderGroup) (domain.ProviderGroup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return domain.ProviderGroup{}, err
	}
	var err error
	if value, err = normalizeProviderGroup(value); err != nil {
		return domain.ProviderGroup{}, err
	}
	now := time.Now().UTC()
	var body []byte
	if scanErr := s.db.QueryRow(`SELECT body FROM provider_groups WHERE id = ?`, value.ID).Scan(&body); scanErr == nil {
		var old domain.ProviderGroup
		if err = json.Unmarshal(body, &old); err != nil {
			return domain.ProviderGroup{}, err
		}
		value.CreatedAt = old.CreatedAt
		if value.Advice == nil {
			value.Advice = old.Advice
		}
	} else {
		var count int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM provider_groups`).Scan(&count); err != nil {
			return domain.ProviderGroup{}, err
		}
		if count >= maxProviderGroups {
			return domain.ProviderGroup{}, errors.New("provider group limit reached")
		}
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	body, err = json.Marshal(value)
	if err != nil {
		return domain.ProviderGroup{}, err
	}
	_, err = s.db.Exec(`INSERT INTO provider_groups(id, body, updated_at_ns) VALUES(?, ?, ?) ON CONFLICT(id) DO UPDATE SET body=excluded.body, updated_at_ns=excluded.updated_at_ns`, value.ID, body, now.UnixNano())
	return value, err
}

func (s *JSON) DeleteProviderGroup(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM provider_groups WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count > 0, nil
}

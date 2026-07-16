package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
)

const maxIncidents = 5000

func normalizeIncident(value domain.Incident) (domain.Incident, error) {
	value.ID = strings.TrimSpace(value.ID)
	value.SubjectType = strings.TrimSpace(value.SubjectType)
	value.SubjectID = strings.TrimSpace(value.SubjectID)
	value.Title = strings.TrimSpace(value.Title)
	value.Note = strings.TrimSpace(value.Note)
	if value.ID == "" {
		value.ID = "incident-" + randomHex(12)
	}
	if value.SubjectType != "provider" && value.SubjectType != "group" {
		return value, errors.New("incident subjectType must be provider or group")
	}
	if value.SubjectID == "" || len(value.SubjectID) > 256 {
		return value, errors.New("incident subjectId is required")
	}
	if value.Title == "" || len(value.Title) > 256 {
		return value, errors.New("incident title is required")
	}
	if value.Status != "open" && value.Status != "acknowledged" && value.Status != "muted" && value.Status != "resolved" {
		return value, errors.New("invalid incident status")
	}
	if value.Severity != "warning" && value.Severity != "critical" {
		return value, errors.New("invalid incident severity")
	}
	if len(value.Note) > 4096 {
		return value, errors.New("incident note must not exceed 4096 bytes")
	}
	if value.ErrorCounts == nil {
		value.ErrorCounts = map[string]int{}
	}
	value.JobIDs = uniqueStrings(value.JobIDs, 100)
	value.RequestIDs = uniqueStrings(value.RequestIDs, 100)
	if len(value.Timeline) > 200 {
		value.Timeline = value.Timeline[len(value.Timeline)-200:]
	}
	return value, nil
}

func uniqueStrings(values []string, limit int) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
			if len(result) >= limit {
				break
			}
		}
	}
	return result
}
func sortIncidents(values []domain.Incident) {
	sort.Slice(values, func(i, j int) bool { return values[i].UpdatedAt.After(values[j].UpdatedAt) })
}

func (s *JSON) applyIncidentsV14() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS incidents (id TEXT PRIMARY KEY, body BLOB NOT NULL, updated_at_ns INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(14, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *JSON) ListIncidents(status string) ([]domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	query := `SELECT body FROM incidents`
	args := []any{}
	if status != "" {
		query += ` WHERE json_extract(body, '$.status') = ?`
		args = append(args, status)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.Incident{}
	for rows.Next() {
		var body []byte
		var value domain.Incident
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	sortIncidents(values)
	return values, rows.Err()
}
func (s *JSON) GetIncident(id string) (domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getIncidentUnlocked(id)
}
func (s *JSON) getIncidentUnlocked(id string) (domain.Incident, error) {
	if err := s.ready(); err != nil {
		return domain.Incident{}, err
	}
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM incidents WHERE id = ?`, strings.TrimSpace(id)).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Incident{}, fs.ErrNotExist
		}
		return domain.Incident{}, err
	}
	var value domain.Incident
	return value, json.Unmarshal(body, &value)
}
func (s *JSON) FindOpenIncident(subjectType, subjectID string) (domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body []byte
	err := s.db.QueryRow(`SELECT body FROM incidents WHERE json_extract(body, '$.subjectType') = ? AND json_extract(body, '$.subjectId') = ? AND json_extract(body, '$.status') != 'resolved' ORDER BY updated_at_ns DESC LIMIT 1`, subjectType, subjectID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Incident{}, fs.ErrNotExist
	}
	if err != nil {
		return domain.Incident{}, err
	}
	var value domain.Incident
	return value, json.Unmarshal(body, &value)
}
func (s *JSON) UpsertIncident(value domain.Incident) (domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if value, err = normalizeIncident(value); err != nil {
		return value, err
	}
	now := time.Now().UTC()
	if value.StartedAt.IsZero() {
		value.StartedAt = now
	}
	value.UpdatedAt = now
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM incidents`).Scan(&count); err != nil {
		return value, err
	}
	if count >= maxIncidents {
		_, _ = s.db.Exec(`DELETE FROM incidents WHERE id IN (SELECT id FROM incidents WHERE json_extract(body, '$.status') = 'resolved' ORDER BY updated_at_ns ASC LIMIT 100)`)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	_, err = s.db.Exec(`INSERT INTO incidents(id,body,updated_at_ns) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,updated_at_ns=excluded.updated_at_ns`, value.ID, body, now.UnixNano())
	return value, err
}

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"time"

	"ai-watch/internal/domain"
)

func normalizeIncidentPostmortem(value domain.IncidentPostmortem) (domain.IncidentPostmortem, error) {
	value.IncidentID, value.Title, value.Subject = strings.TrimSpace(value.IncidentID), strings.TrimSpace(value.Title), strings.TrimSpace(value.Subject)
	value.RootCause, value.Mitigation, value.Owner = strings.TrimSpace(value.RootCause), strings.TrimSpace(value.Mitigation), strings.TrimSpace(value.Owner)
	if value.IncidentID == "" || len(value.IncidentID) > 256 {
		return value, errors.New("postmortem incidentId is required")
	}
	if value.Status != "draft" && value.Status != "completed" {
		return value, errors.New("postmortem status must be draft or completed")
	}
	if value.Title == "" || len(value.Title) > 256 || len(value.Subject) > 256 {
		return value, errors.New("postmortem title or subject is invalid")
	}
	if len(value.RootCause) > 8192 || len(value.Mitigation) > 8192 || len(value.Owner) > 256 {
		return value, errors.New("postmortem editable fields are too large")
	}
	if len(value.Actions) > 50 {
		return value, errors.New("postmortem actions must not exceed 50")
	}
	for index := range value.Actions {
		value.Actions[index].Text, value.Actions[index].Owner = strings.TrimSpace(value.Actions[index].Text), strings.TrimSpace(value.Actions[index].Owner)
		if value.Actions[index].Text == "" || len(value.Actions[index].Text) > 2048 || len(value.Actions[index].Owner) > 256 {
			return value, errors.New("postmortem action is invalid")
		}
	}
	value.JobIDs, value.RequestIDs = uniqueStrings(value.JobIDs, 100), uniqueStrings(value.RequestIDs, 100)
	if len(value.Timeline) > 200 {
		value.Timeline = value.Timeline[len(value.Timeline)-200:]
	}
	if value.ErrorCounts == nil {
		value.ErrorCounts = map[string]int{}
	}
	return value, nil
}

func (s *JSON) applyIncidentPostmortemsV16() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS incident_postmortems (incident_id TEXT PRIMARY KEY, body BLOB NOT NULL, updated_at_ns INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(16, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *JSON) GetIncidentPostmortem(id string) (domain.IncidentPostmortem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM incident_postmortems WHERE incident_id = ?`, strings.TrimSpace(id)).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.IncidentPostmortem{}, fs.ErrNotExist
		}
		return domain.IncidentPostmortem{}, err
	}
	var value domain.IncidentPostmortem
	return value, json.Unmarshal(body, &value)
}

func (s *JSON) UpsertIncidentPostmortem(value domain.IncidentPostmortem) (domain.IncidentPostmortem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if value, err = normalizeIncidentPostmortem(value); err != nil {
		return value, err
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	body, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	_, err = s.db.Exec(`INSERT INTO incident_postmortems(incident_id,body,updated_at_ns) VALUES(?,?,?) ON CONFLICT(incident_id) DO UPDATE SET body=excluded.body,updated_at_ns=excluded.updated_at_ns`, value.IncidentID, body, now.UnixNano())
	return value, err
}

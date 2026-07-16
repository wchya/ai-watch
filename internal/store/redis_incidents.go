package store

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"github.com/redis/go-redis/v9"
)

func (r *Redis) incidentKey() string { return r.key("incidents") }
func (r *Redis) ListIncidents(status string) ([]domain.Incident, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	raw, err := r.client.HVals(context.Background(), r.incidentKey()).Result()
	if err != nil {
		return nil, err
	}
	values := []domain.Incident{}
	for _, item := range raw {
		var value domain.Incident
		if err = json.Unmarshal([]byte(item), &value); err != nil {
			return nil, err
		}
		if status == "" || value.Status == status {
			values = append(values, value)
		}
	}
	sortIncidents(values)
	return values, nil
}
func (r *Redis) GetIncident(id string) (domain.Incident, error) {
	if err := r.ready(); err != nil {
		return domain.Incident{}, err
	}
	raw, err := r.client.HGet(context.Background(), r.incidentKey(), strings.TrimSpace(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.Incident{}, fs.ErrNotExist
	}
	if err != nil {
		return domain.Incident{}, err
	}
	var value domain.Incident
	return value, json.Unmarshal(raw, &value)
}
func (r *Redis) FindOpenIncident(subjectType, subjectID string) (domain.Incident, error) {
	values, err := r.ListIncidents("")
	if err != nil {
		return domain.Incident{}, err
	}
	for _, value := range values {
		if value.SubjectType == subjectType && value.SubjectID == subjectID && value.Status != "resolved" {
			return value, nil
		}
	}
	return domain.Incident{}, fs.ErrNotExist
}
func (r *Redis) UpsertIncident(value domain.Incident) (domain.Incident, error) {
	if err := r.ready(); err != nil {
		return value, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	var err error
	if value, err = normalizeIncident(value); err != nil {
		return value, err
	}
	now := time.Now().UTC()
	if value.StartedAt.IsZero() {
		value.StartedAt = now
	}
	value.UpdatedAt = now
	body, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	return value, r.client.HSet(context.Background(), r.incidentKey(), value.ID, body).Err()
}

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

func (r *Redis) incidentPostmortemKey() string { return r.key("incident-postmortems") }
func (r *Redis) GetIncidentPostmortem(id string) (domain.IncidentPostmortem, error) {
	raw, err := r.client.HGet(context.Background(), r.incidentPostmortemKey(), strings.TrimSpace(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.IncidentPostmortem{}, fs.ErrNotExist
	}
	if err != nil {
		return domain.IncidentPostmortem{}, err
	}
	var value domain.IncidentPostmortem
	return value, json.Unmarshal(raw, &value)
}
func (r *Redis) UpsertIncidentPostmortem(value domain.IncidentPostmortem) (domain.IncidentPostmortem, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
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
	return value, r.client.HSet(context.Background(), r.incidentPostmortemKey(), value.IncidentID, body).Err()
}

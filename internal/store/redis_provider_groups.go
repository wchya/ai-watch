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

func (r *Redis) providerGroupKey() string { return r.key("provider-groups") }

func (r *Redis) ListProviderGroups() ([]domain.ProviderGroup, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	raw, err := r.client.HVals(context.Background(), r.providerGroupKey()).Result()
	if err != nil {
		return nil, err
	}
	values := make([]domain.ProviderGroup, 0, len(raw))
	for _, item := range raw {
		var value domain.ProviderGroup
		if err = json.Unmarshal([]byte(item), &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	sortProviderGroups(values)
	return values, nil
}
func (r *Redis) GetProviderGroup(id string) (domain.ProviderGroup, error) {
	if err := r.ready(); err != nil {
		return domain.ProviderGroup{}, err
	}
	raw, err := r.client.HGet(context.Background(), r.providerGroupKey(), strings.TrimSpace(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.ProviderGroup{}, fs.ErrNotExist
	}
	if err != nil {
		return domain.ProviderGroup{}, err
	}
	var value domain.ProviderGroup
	return value, json.Unmarshal(raw, &value)
}
func (r *Redis) UpsertProviderGroup(value domain.ProviderGroup) (domain.ProviderGroup, error) {
	if err := r.ready(); err != nil {
		return domain.ProviderGroup{}, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	var err error
	if value, err = normalizeProviderGroup(value); err != nil {
		return domain.ProviderGroup{}, err
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if old, getErr := r.GetProviderGroup(value.ID); getErr == nil {
		value.CreatedAt = old.CreatedAt
		if value.Advice == nil {
			value.Advice = old.Advice
		}
	} else {
		count, countErr := r.client.HLen(ctx, r.providerGroupKey()).Result()
		if countErr != nil {
			return domain.ProviderGroup{}, countErr
		}
		if count >= maxProviderGroups {
			return domain.ProviderGroup{}, errors.New("provider group limit reached")
		}
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	body, err := json.Marshal(value)
	if err != nil {
		return domain.ProviderGroup{}, err
	}
	err = r.client.HSet(ctx, r.providerGroupKey(), value.ID, body).Err()
	return value, err
}
func (r *Redis) DeleteProviderGroup(id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	deleted, err := r.client.HDel(context.Background(), r.providerGroupKey(), strings.TrimSpace(id)).Result()
	return deleted > 0, err
}

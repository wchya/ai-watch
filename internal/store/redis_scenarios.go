package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"github.com/redis/go-redis/v9"
)

func (r *Redis) scenarioKey() string { return r.key("test-scenarios") }

func (r *Redis) seedTestScenarios(ctx context.Context) error {
	now := time.Now().UTC()
	pipe := r.client.TxPipeline()
	for _, value := range defaultTestScenarios() {
		value.CreatedAt, value.UpdatedAt = now, now
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		pipe.HSetNX(ctx, r.scenarioKey(), value.ID, body)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) ListTestScenarios() ([]domain.TestScenario, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	ctx := context.Background()
	if err := r.seedTestScenarios(ctx); err != nil {
		return nil, fmt.Errorf("seed test scenarios: %w", err)
	}
	raw, err := r.client.HVals(ctx, r.scenarioKey()).Result()
	if err != nil {
		return nil, err
	}
	values := make([]domain.TestScenario, 0, len(raw))
	for _, item := range raw {
		var value domain.TestScenario
		if err = json.Unmarshal([]byte(item), &value); err != nil {
			return nil, fmt.Errorf("decode test scenario: %w", err)
		}
		values = append(values, value)
	}
	sortTestScenarios(values)
	return values, nil
}

func (r *Redis) GetTestScenario(id string) (domain.TestScenario, error) {
	if err := r.ready(); err != nil {
		return domain.TestScenario{}, err
	}
	ctx := context.Background()
	if err := r.seedTestScenarios(ctx); err != nil {
		return domain.TestScenario{}, err
	}
	raw, err := r.client.HGet(ctx, r.scenarioKey(), strings.TrimSpace(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.TestScenario{}, fs.ErrNotExist
	}
	if err != nil {
		return domain.TestScenario{}, err
	}
	var value domain.TestScenario
	return value, json.Unmarshal(raw, &value)
}

func (r *Redis) UpsertTestScenario(value domain.TestScenario) (domain.TestScenario, error) {
	if err := r.ready(); err != nil {
		return domain.TestScenario{}, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	var err error
	if value, err = normalizeTestScenario(value); err != nil {
		return domain.TestScenario{}, err
	}
	ctx := context.Background()
	if err = r.seedTestScenarios(ctx); err != nil {
		return domain.TestScenario{}, err
	}
	now := time.Now().UTC()
	if old, getErr := r.GetTestScenario(value.ID); getErr == nil {
		value.CreatedAt, value.BuiltIn = old.CreatedAt, old.BuiltIn
	} else {
		count, countErr := r.client.HLen(ctx, r.scenarioKey()).Result()
		if countErr != nil {
			return domain.TestScenario{}, countErr
		}
		if count >= maxTestScenarios {
			return domain.TestScenario{}, errors.New("test scenario limit reached")
		}
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	body, err := json.Marshal(value)
	if err != nil {
		return domain.TestScenario{}, err
	}
	if err = r.client.HSet(ctx, r.scenarioKey(), value.ID, body).Err(); err != nil {
		return domain.TestScenario{}, err
	}
	return value, nil
}

func (r *Redis) DeleteTestScenario(id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	value, err := r.GetTestScenario(strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if value.BuiltIn {
		return false, errors.New("built-in test scenario cannot be deleted")
	}
	deleted, err := r.client.HDel(context.Background(), r.scenarioKey(), value.ID).Result()
	return deleted > 0, err
}

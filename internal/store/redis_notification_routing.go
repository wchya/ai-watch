package store

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"github.com/redis/go-redis/v9"
)

func (r *Redis) notificationChannelKey() string { return r.key("notification-channels") }
func (r *Redis) notificationRoutesKey() string  { return r.key("notification-routes") }
func (r *Redis) ListNotificationChannels() ([]domain.NotificationChannel, error) {
	raw, err := r.client.HVals(context.Background(), r.notificationChannelKey()).Result()
	if err != nil {
		return nil, err
	}
	values := []domain.NotificationChannel{}
	for _, item := range raw {
		var record notificationChannelRecord
		if err = json.Unmarshal([]byte(item), &record); err != nil {
			return nil, err
		}
		webhook, decryptErr := r.decrypt(record.Secret)
		if decryptErr != nil {
			return nil, decryptErr
		}
		values = append(values, channelFromRecord(record, webhook))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, nil
}
func (r *Redis) GetNotificationChannel(id string) (domain.NotificationChannel, error) {
	raw, err := r.client.HGet(context.Background(), r.notificationChannelKey(), strings.TrimSpace(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.NotificationChannel{}, fs.ErrNotExist
	}
	if err != nil {
		return domain.NotificationChannel{}, err
	}
	var record notificationChannelRecord
	if err = json.Unmarshal(raw, &record); err != nil {
		return domain.NotificationChannel{}, err
	}
	webhook, err := r.decrypt(record.Secret)
	if err != nil {
		return domain.NotificationChannel{}, err
	}
	return channelFromRecord(record, webhook), nil
}
func (r *Redis) UpsertNotificationChannel(value domain.NotificationChannel) (domain.NotificationChannel, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	secret, err := r.encrypt(value.WebhookURL)
	if err != nil {
		return value, err
	}
	record := notificationChannelRecord{ID: value.ID, Name: value.Name, Description: value.Description, Type: value.Type, Enabled: value.Enabled, Secret: secret, CreatedAt: value.CreatedAt, UpdatedAt: now}
	body, err := json.Marshal(record)
	if err != nil {
		return value, err
	}
	return channelFromRecord(record, value.WebhookURL), r.client.HSet(context.Background(), r.notificationChannelKey(), value.ID, body).Err()
}
func (r *Redis) DeleteNotificationChannel(id string) (bool, error) {
	count, err := r.client.HDel(context.Background(), r.notificationChannelKey(), strings.TrimSpace(id)).Result()
	return count > 0, err
}
func (r *Redis) LoadNotificationRoutes() (domain.NotificationRoutes, error) {
	raw, err := r.client.Get(context.Background(), r.notificationRoutesKey()).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.NotificationRoutes{Routes: map[string]string{}}, nil
	}
	if err != nil {
		return domain.NotificationRoutes{}, err
	}
	var value domain.NotificationRoutes
	err = json.Unmarshal(raw, &value)
	if value.Routes == nil {
		value.Routes = map[string]string{}
	}
	return value, err
}
func (r *Redis) SaveNotificationRoutes(value domain.NotificationRoutes) (domain.NotificationRoutes, error) {
	if value.Routes == nil {
		value.Routes = map[string]string{}
	}
	value.UpdatedAt = time.Now().UTC()
	body, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	return value, r.client.Set(context.Background(), r.notificationRoutesKey(), body, 0).Err()
}

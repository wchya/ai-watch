package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/security"
)

type notificationChannelRecord struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type"`
	Enabled     bool           `json:"enabled"`
	Secret      encryptedValue `json:"secret"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

func (s *JSON) applyNotificationRoutingV17() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS notification_channels (id TEXT PRIMARY KEY, body BLOB NOT NULL, updated_at_ns INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS notification_routes (id INTEGER PRIMARY KEY CHECK(id=1), body BLOB NOT NULL, updated_at_ns INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(17, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *JSON) encryptNotification(value string) (encryptedValue, error) {
	if s.aead == nil {
		return encryptedValue{}, errors.New("secure configuration encryption is unavailable")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return encryptedValue{}, err
	}
	ciphertext := s.aead.Seal(nil, nonce, []byte(value), []byte("sqlite-notification-channel"))
	return encryptedValue{Version: 1, Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext)}, nil
}
func (s *JSON) decryptNotification(value encryptedValue) (string, error) {
	if s.aead == nil {
		return "", errors.New("secure configuration encryption is unavailable")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(value.Nonce)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(value.Ciphertext)
	if err != nil {
		return "", err
	}
	plain, err := s.aead.Open(nil, nonce, ciphertext, []byte("sqlite-notification-channel"))
	return string(plain), err
}
func channelFromRecord(record notificationChannelRecord, webhook string) domain.NotificationChannel {
	return domain.NotificationChannel{ID: record.ID, Name: record.Name, Description: record.Description, Type: record.Type, Enabled: record.Enabled, WebhookURL: webhook, Configured: webhook != "", MaskedWebhook: security.Mask(webhook), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func (s *JSON) ListNotificationChannels() ([]domain.NotificationChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT body FROM notification_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []domain.NotificationChannel{}
	for rows.Next() {
		var body []byte
		var record notificationChannelRecord
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &record); err != nil {
			return nil, err
		}
		webhook, decryptErr := s.decryptNotification(record.Secret)
		if decryptErr != nil {
			return nil, decryptErr
		}
		values = append(values, channelFromRecord(record, webhook))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
	return values, rows.Err()
}
func (s *JSON) GetNotificationChannel(id string) (domain.NotificationChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM notification_channels WHERE id=?`, strings.TrimSpace(id)).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NotificationChannel{}, fs.ErrNotExist
		}
		return domain.NotificationChannel{}, err
	}
	var record notificationChannelRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return domain.NotificationChannel{}, err
	}
	webhook, err := s.decryptNotification(record.Secret)
	if err != nil {
		return domain.NotificationChannel{}, err
	}
	return channelFromRecord(record, webhook), nil
}
func (s *JSON) UpsertNotificationChannel(value domain.NotificationChannel) (domain.NotificationChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	secret, err := s.encryptNotification(value.WebhookURL)
	if err != nil {
		return value, err
	}
	record := notificationChannelRecord{ID: value.ID, Name: value.Name, Description: value.Description, Type: value.Type, Enabled: value.Enabled, Secret: secret, CreatedAt: value.CreatedAt, UpdatedAt: now}
	body, err := json.Marshal(record)
	if err != nil {
		return value, err
	}
	_, err = s.db.Exec(`INSERT INTO notification_channels(id,body,updated_at_ns) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,updated_at_ns=excluded.updated_at_ns`, value.ID, body, now.UnixNano())
	return channelFromRecord(record, value.WebhookURL), err
}
func (s *JSON) DeleteNotificationChannel(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`DELETE FROM notification_channels WHERE id=?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count > 0, nil
}
func (s *JSON) LoadNotificationRoutes() (domain.NotificationRoutes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body []byte
	if err := s.db.QueryRow(`SELECT body FROM notification_routes WHERE id=1`).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NotificationRoutes{Routes: map[string]string{}}, nil
		}
		return domain.NotificationRoutes{}, err
	}
	var value domain.NotificationRoutes
	err := json.Unmarshal(body, &value)
	if value.Routes == nil {
		value.Routes = map[string]string{}
	}
	return value, err
}
func (s *JSON) SaveNotificationRoutes(value domain.NotificationRoutes) (domain.NotificationRoutes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value.Routes == nil {
		value.Routes = map[string]string{}
	}
	value.UpdatedAt = time.Now().UTC()
	body, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	_, err = s.db.Exec(`INSERT INTO notification_routes(id,body,updated_at_ns) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body,updated_at_ns=excluded.updated_at_ns`, body, value.UpdatedAt.UnixNano())
	return value, err
}

package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/security"

	"github.com/redis/go-redis/v9"
)

const redisSchemaVersion = 1

type IdempotencyRecord struct {
	Fingerprint string            `json:"fingerprint"`
	Pending     bool              `json:"pending"`
	Status      int               `json:"status,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        []byte            `json:"body,omitempty"`
}

func (r *Redis) ClaimIdempotency(key, fingerprint string, ttl time.Duration) (IdempotencyRecord, bool, error) {
	record := IdempotencyRecord{Fingerprint: fingerprint, Pending: true}
	body, err := json.Marshal(record)
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	redisKey := r.key("idempotency:" + key)
	claimed, err := r.client.SetNX(context.Background(), redisKey, body, ttl).Result()
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	if claimed {
		return record, true, nil
	}
	raw, err := r.client.Get(context.Background(), redisKey).Bytes()
	if err != nil {
		return IdempotencyRecord{}, false, err
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return IdempotencyRecord{}, false, err
	}
	return record, false, nil
}

func (r *Redis) CompleteIdempotency(key string, record IdempotencyRecord, ttl time.Duration) error {
	record.Pending = false
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return r.client.Set(context.Background(), r.key("idempotency:"+key), body, ttl).Err()
}

func (r *Redis) ReadIdempotency(key string) (IdempotencyRecord, error) {
	raw, err := r.client.Get(context.Background(), r.key("idempotency:"+key)).Bytes()
	if err != nil {
		return IdempotencyRecord{}, err
	}
	var record IdempotencyRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return IdempotencyRecord{}, err
	}
	return record, nil
}

type Redis struct {
	mu        sync.RWMutex
	writeMu   sync.Mutex
	prewarmMu sync.Mutex
	client    redis.UniversalClient
	prefix    string
	dataDir   string
	initErr   error
	aead      cipher.AEAD
	settings  domain.Settings
	summaries []domain.Summary
	examples  []domain.ProviderExample
	schedules []domain.Schedule
	manual    []domain.ManualProvider
	ccSwitch  []domain.CCSwitchProvider
	dingTalk  domain.DingTalkConfig
}

func NewRedis(dataDir, redisURL string) *Redis {
	prefix := strings.TrimSpace(os.Getenv("AI_WATCH_REDIS_PREFIX"))
	if prefix == "" {
		prefix = "ai-watch"
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return &Redis{initErr: fmt.Errorf("parse Redis URL: %w", err)}
	}
	configuredKey := os.Getenv("AI_WATCH_MASTER_KEY")
	if configuredKey == "" {
		configuredKey = os.Getenv("AI_WATCH_ENCRYPTION_KEY") // legacy name
	}
	key, err := loadEncryptionKey(dataDir, configuredKey)
	if err != nil {
		return &Redis{initErr: err}
	}
	return NewRedisWithClient(dataDir, prefix, redis.NewClient(options), key)
}

func NewRedisWithClient(dataDir, prefix string, client redis.UniversalClient, encryptionKey []byte) *Redis {
	r := &Redis{client: client, prefix: strings.Trim(strings.TrimSpace(prefix), ":"), dataDir: dataDir}
	if r.prefix == "" {
		r.prefix = "ai-watch"
	}
	if len(encryptionKey) == 32 {
		if block, err := aes.NewCipher(encryptionKey); err == nil {
			r.aead, _ = cipher.NewGCM(block)
		}
	}
	r.initErr = r.open()
	return r
}

func (r *Redis) key(parts ...string) string { return r.prefix + ":" + strings.Join(parts, ":") }

func (r *Redis) open() error {
	if r.client == nil {
		return errors.New("Redis client is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("connect Redis: %w", err)
	}
	if err := r.verifyReadWrite(ctx); err != nil {
		return err
	}
	if err := r.initializeSchema(ctx); err != nil {
		return err
	}
	if err := r.migrateSQLite(ctx); err != nil {
		return err
	}
	if err := r.seedDefaults(ctx); err != nil {
		return err
	}
	_, err := r.Prewarm(ctx)
	return err
}

func (r *Redis) verifyReadWrite(ctx context.Context) error {
	key := r.key("readiness", randomHex(8))
	value := randomHex(16)
	if err := r.client.Set(ctx, key, value, 15*time.Second).Err(); err != nil {
		return fmt.Errorf("verify Redis write access: %w", err)
	}
	defer r.client.Del(context.Background(), key)
	loaded, err := r.client.Get(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("verify Redis read access: %w", err)
	}
	if loaded != value {
		return errors.New("verify Redis read/write access: probe value mismatch")
	}
	if err = r.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("verify Redis delete access: %w", err)
	}
	return nil
}

func (r *Redis) initializeSchema(ctx context.Context) error {
	key := r.key("schema", "version")
	if err := r.client.SetNX(ctx, key, redisSchemaVersion, 0).Err(); err != nil {
		return fmt.Errorf("initialize Redis schema metadata: %w", err)
	}
	version, err := r.client.Get(ctx, key).Int()
	if err != nil {
		return fmt.Errorf("read Redis schema metadata: %w", err)
	}
	if version != redisSchemaVersion {
		return fmt.Errorf("unsupported Redis schema version %d", version)
	}
	return nil
}

func (r *Redis) Err() error { return r.initErr }

func (r *Redis) ready() error {
	if r.initErr != nil {
		return r.initErr
	}
	if r.client == nil {
		return errors.New("Redis store is closed")
	}
	return nil
}

func (r *Redis) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		return r.initErr
	}
	err := r.client.Close()
	r.client = nil
	return err
}

type RedisPrewarmResult struct {
	Settings          int `json:"settings"`
	Summaries         int `json:"summaries"`
	ProviderExamples  int `json:"providerExamples"`
	Schedules         int `json:"schedules"`
	ManualProviders   int `json:"manualProviders"`
	CCSwitchProviders int `json:"ccSwitchProviders"`
	DingTalk          int `json:"dingTalk"`
}

func (r *Redis) AdminClient() redis.UniversalClient { return r.client }

func (r *Redis) Prefix() string { return r.prefix }

func (r *Redis) Prewarm(ctx context.Context) (RedisPrewarmResult, error) {
	r.prewarmMu.Lock()
	defer r.prewarmMu.Unlock()
	if err := r.ready(); err != nil {
		return RedisPrewarmResult{}, err
	}
	settings, err := r.loadSettingsRedis(ctx)
	if err != nil {
		return RedisPrewarmResult{}, err
	}
	summaries, err := r.loadSummariesRedis(ctx)
	if err != nil {
		return RedisPrewarmResult{}, err
	}
	examples, err := r.listExamplesRedis(ctx)
	if err != nil {
		return RedisPrewarmResult{}, err
	}
	schedules, err := r.listSchedulesRedis(ctx)
	if err != nil {
		return RedisPrewarmResult{}, err
	}
	manual, err := r.listManualProvidersRedis(ctx)
	if err != nil {
		return RedisPrewarmResult{}, err
	}
	ccSwitch, err := r.listCCSwitchProvidersRedis(ctx)
	if err != nil {
		return RedisPrewarmResult{}, err
	}
	dingTalk, err := r.loadDingTalkConfigRedis(ctx)
	if err != nil {
		return RedisPrewarmResult{}, err
	}
	r.mu.Lock()
	r.settings, r.summaries, r.examples, r.schedules = settings, summaries, examples, schedules
	r.manual, r.ccSwitch, r.dingTalk = manual, ccSwitch, dingTalk
	r.mu.Unlock()
	dingTalkCount := 0
	if dingTalk.Configured {
		dingTalkCount = 1
	}
	return RedisPrewarmResult{
		Settings:          1,
		Summaries:         len(summaries),
		ProviderExamples:  len(examples),
		Schedules:         len(schedules),
		ManualProviders:   len(manual),
		CCSwitchProviders: len(ccSwitch),
		DingTalk:          dingTalkCount,
	}, nil
}

func (r *Redis) LoadSettings() (domain.Settings, error) {
	if err := r.ready(); err != nil {
		return domain.Settings{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.settings, nil
}

func (r *Redis) SaveSettings(value domain.Settings) error {
	if err := r.ready(); err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err = r.client.Set(context.Background(), r.key("settings"), b, 0).Err(); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	r.mu.Lock()
	r.settings = value
	r.mu.Unlock()
	return nil
}

func (r *Redis) loadSettingsRedis(ctx context.Context) (domain.Settings, error) {
	b, err := r.client.Get(ctx, r.key("settings")).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.DefaultSettings(), nil
	}
	if err != nil {
		return domain.Settings{}, fmt.Errorf("load settings: %w", err)
	}
	var value domain.Settings
	if err = json.Unmarshal(b, &value); err != nil {
		return domain.Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	return value, nil
}

func (r *Redis) ListProviderExamples() ([]domain.ProviderExample, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.ProviderExample, len(r.examples))
	copy(result, r.examples)
	return result, nil
}

func (r *Redis) UpsertProviderExample(value domain.ProviderExample) (domain.ProviderExample, error) {
	if err := r.ready(); err != nil {
		return domain.ProviderExample{}, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	var err error
	if value, err = normalizeProviderExample(value); err != nil {
		return domain.ProviderExample{}, err
	}
	value.UpdatedAt = time.Now().UTC()
	if err = r.hsetJSON(context.Background(), r.key("provider-examples"), value.ID, value); err != nil {
		return domain.ProviderExample{}, err
	}
	r.cacheExample(value)
	return value, nil
}

func (r *Redis) DeleteProviderExample(id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	id = strings.TrimSpace(id)
	if !providerExampleID.MatchString(id) {
		return false, errors.New("invalid provider example id")
	}
	deleted, err := r.client.HDel(context.Background(), r.key("provider-examples"), id).Result()
	if err == nil && deleted > 0 {
		r.removeCachedExample(id)
	}
	return deleted > 0, err
}

func (r *Redis) listExamplesRedis(ctx context.Context) ([]domain.ProviderExample, error) {
	var values []domain.ProviderExample
	if err := r.hvalsJSON(ctx, r.key("provider-examples"), &values); err != nil {
		return nil, err
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CLI != values[j].CLI {
			return values[i].CLI == domain.CLICodex
		}
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].ID < values[j].ID
	})
	return values, nil
}

func (r *Redis) refreshExamples(ctx context.Context) error {
	values, err := r.listExamplesRedis(ctx)
	if err == nil {
		r.mu.Lock()
		r.examples = values
		r.mu.Unlock()
	}
	return err
}

func (r *Redis) cacheExample(value domain.ProviderExample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for index := range r.examples {
		if r.examples[index].ID == value.ID {
			r.examples[index] = value
			found = true
			break
		}
	}
	if !found {
		r.examples = append(r.examples, value)
	}
	sort.Slice(r.examples, func(i, j int) bool {
		if r.examples[i].CLI != r.examples[j].CLI {
			return r.examples[i].CLI == domain.CLICodex
		}
		if r.examples[i].Name != r.examples[j].Name {
			return r.examples[i].Name < r.examples[j].Name
		}
		return r.examples[i].ID < r.examples[j].ID
	})
}

func (r *Redis) removeCachedExample(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.examples {
		if r.examples[index].ID == id {
			r.examples = append(r.examples[:index], r.examples[index+1:]...)
			return
		}
	}
}

func (r *Redis) ListSchedules() ([]domain.Schedule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.Schedule, len(r.schedules))
	copy(result, r.schedules)
	return result, nil
}

func (r *Redis) GetSchedule(id string) (domain.Schedule, error) {
	if err := r.ready(); err != nil {
		return domain.Schedule{}, err
	}
	var value domain.Schedule
	err := r.hgetJSON(context.Background(), r.key("schedules"), strings.TrimSpace(id), &value)
	if errors.Is(err, redis.Nil) {
		return domain.Schedule{}, fs.ErrNotExist
	}
	return value, err
}

var upsertScheduleScript = redis.NewScript(`
local raw = redis.call('HGET', KEYS[1], ARGV[1])
local value = cjson.decode(ARGV[2])
if raw then
  local old = cjson.decode(raw)
  value.createdAt = old.createdAt
  value.lastOccurrenceKey = old.lastOccurrenceKey
  value.lastStatus = old.lastStatus
  value.lastJobId = old.lastJobId
  value.lastOccurrenceAt = old.lastOccurrenceAt
else
  if redis.call('HLEN', KEYS[1]) >= tonumber(ARGV[3]) then
    return redis.error_reply('schedule limit reached')
  end
end
local encoded = cjson.encode(value)
redis.call('HSET', KEYS[1], ARGV[1], encoded)
return encoded
`)

var markScheduleRunScript = redis.NewScript(`
local raw = redis.call('HGET', KEYS[1], ARGV[1])
if not raw then return false end
local value = cjson.decode(raw)
value.lastOccurrenceKey = ARGV[2]
value.lastStatus = ARGV[3]
value.lastJobId = ARGV[4]
value.lastOccurrenceAt = ARGV[5]
value.updatedAt = ARGV[6]
local encoded = cjson.encode(value)
redis.call('HSET', KEYS[1], ARGV[1], encoded)
return encoded
`)

func (r *Redis) UpsertSchedule(value domain.Schedule) (domain.Schedule, error) {
	if err := r.ready(); err != nil {
		return domain.Schedule{}, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	var err error
	if value, err = normalizeSchedule(value); err != nil {
		return domain.Schedule{}, err
	}
	now := time.Now().UTC()
	if value.ID == "" {
		value.ID = "schedule-" + randomHex(8)
	}
	value.CreatedAt = now
	value.UpdatedAt = now
	body, err := json.Marshal(value)
	if err != nil {
		return domain.Schedule{}, err
	}
	raw, err := upsertScheduleScript.Run(context.Background(), r.client, []string{r.key("schedules")}, value.ID, body, maxSchedules).Text()
	if err != nil {
		if strings.Contains(err.Error(), ErrScheduleLimit.Error()) {
			return domain.Schedule{}, ErrScheduleLimit
		}
		return domain.Schedule{}, fmt.Errorf("save schedule: %w", err)
	}
	if err = json.Unmarshal([]byte(raw), &value); err != nil {
		return domain.Schedule{}, fmt.Errorf("decode saved schedule: %w", err)
	}
	r.cacheSchedule(value)
	return value, nil
}

func (r *Redis) DeleteSchedule(id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	id = strings.TrimSpace(id)
	if !scheduleID.MatchString(id) {
		return false, errors.New("invalid schedule id")
	}
	deleted, err := r.client.HDel(context.Background(), r.key("schedules"), id).Result()
	if err == nil && deleted > 0 {
		r.removeCachedSchedule(id)
	}
	return deleted > 0, err
}

func (r *Redis) MarkScheduleRun(id, occurrence, status, jobID string, at time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	now := time.Now().UTC()
	raw, err := markScheduleRunScript.Run(context.Background(), r.client, []string{r.key("schedules")},
		strings.TrimSpace(id), occurrence, status, jobID, at.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)).Text()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mark schedule run: %w", err)
	}
	var value domain.Schedule
	if err = json.Unmarshal([]byte(raw), &value); err != nil {
		return fmt.Errorf("decode marked schedule: %w", err)
	}
	r.cacheSchedule(value)
	return nil
}

func (r *Redis) listSchedulesRedis(ctx context.Context) ([]domain.Schedule, error) {
	var values []domain.Schedule
	if err := r.hvalsJSON(ctx, r.key("schedules"), &values); err != nil {
		return nil, err
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Enabled != values[j].Enabled {
			return values[i].Enabled
		}
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].ID < values[j].ID
	})
	return values, nil
}

func (r *Redis) refreshSchedules(ctx context.Context) error {
	values, err := r.listSchedulesRedis(ctx)
	if err == nil {
		r.mu.Lock()
		r.schedules = values
		r.mu.Unlock()
	}
	return err
}

func (r *Redis) cacheSchedule(value domain.Schedule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for index := range r.schedules {
		if r.schedules[index].ID == value.ID {
			r.schedules[index] = value
			found = true
			break
		}
	}
	if !found {
		r.schedules = append(r.schedules, value)
	}
	sort.Slice(r.schedules, func(i, j int) bool {
		if r.schedules[i].Enabled != r.schedules[j].Enabled {
			return r.schedules[i].Enabled
		}
		if r.schedules[i].Name != r.schedules[j].Name {
			return r.schedules[i].Name < r.schedules[j].Name
		}
		return r.schedules[i].ID < r.schedules[j].ID
	})
}

func (r *Redis) removeCachedSchedule(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.schedules {
		if r.schedules[index].ID == id {
			r.schedules = append(r.schedules[:index], r.schedules[index+1:]...)
			return
		}
	}
}

func (r *Redis) LoadSummaries() ([]domain.Summary, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.Summary, len(r.summaries))
	copy(result, r.summaries)
	return result, nil
}

func (r *Redis) SaveSummary(value domain.Summary, limit int) error {
	if err := r.ready(); err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}
	ctx := context.Background()
	seq, err := r.client.Incr(ctx, r.key("summaries", "seq")).Result()
	if err != nil {
		return err
	}
	pipe := r.client.TxPipeline()
	pipe.HSet(ctx, r.key("summaries", "data"), value.ID, b)
	pipe.ZAdd(ctx, r.key("summaries", "index"), redis.Z{Score: float64(seq), Member: value.ID})
	if _, err = pipe.Exec(ctx); err != nil {
		return fmt.Errorf("save summary: %w", err)
	}
	if limit > 0 {
		excess, countErr := r.client.ZCard(ctx, r.key("summaries", "index")).Result()
		if countErr != nil {
			return fmt.Errorf("count summaries: %w", countErr)
		}
		if excess > int64(limit) {
			ids, trimErr := r.client.ZRange(ctx, r.key("summaries", "index"), 0, excess-int64(limit)-1).Result()
			if trimErr != nil {
				return fmt.Errorf("find summaries to trim: %w", trimErr)
			}
			if len(ids) > 0 {
				pipe = r.client.TxPipeline()
				pipe.ZRem(ctx, r.key("summaries", "index"), stringSliceAny(ids)...)
				pipe.HDel(ctx, r.key("summaries", "data"), ids...)
				if _, err = pipe.Exec(ctx); err != nil {
					return fmt.Errorf("trim summaries: %w", err)
				}
			}
		}
	}
	r.mu.Lock()
	updated := make([]domain.Summary, 0, len(r.summaries)+1)
	updated = append(updated, value)
	for _, existing := range r.summaries {
		if existing.ID != value.ID {
			updated = append(updated, existing)
		}
	}
	if limit > 0 && len(updated) > limit {
		updated = updated[:limit]
	}
	r.summaries = updated
	r.mu.Unlock()
	return nil
}

func (r *Redis) loadSummariesRedis(ctx context.Context) ([]domain.Summary, error) {
	ids, err := r.client.ZRevRange(ctx, r.key("summaries", "index"), 0, -1).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	raw, err := r.client.HMGet(ctx, r.key("summaries", "data"), ids...).Result()
	if err != nil {
		return nil, err
	}
	values := make([]domain.Summary, 0, len(raw))
	for _, item := range raw {
		if item == nil {
			continue
		}
		var value domain.Summary
		if err = json.Unmarshal([]byte(fmt.Sprint(item)), &value); err != nil {
			return nil, fmt.Errorf("decode summary: %w", err)
		}
		values = append(values, value)
	}
	return values, nil
}

var saveEventScript = redis.NewScript(`
local id = redis.call('INCR', KEYS[1])
local body = ARGV[1]
local size = tonumber(ARGV[2])
local score = tonumber(ARGV[3])
redis.call('HSET', KEYS[2], id, body)
redis.call('HSET', KEYS[3], id, size)
redis.call('ZADD', KEYS[4], score, id)
redis.call('INCRBY', KEYS[5], size)
local deleted = 0
local function remove(ids)
  for _, victim in ipairs(ids) do
    local n = tonumber(redis.call('HGET', KEYS[3], victim) or '0')
    redis.call('HDEL', KEYS[2], victim)
    redis.call('HDEL', KEYS[3], victim)
    redis.call('ZREM', KEYS[4], victim)
    redis.call('DECRBY', KEYS[5], n)
    deleted = deleted + 1
  end
end
local cutoff = tonumber(ARGV[4])
if cutoff > 0 then remove(redis.call('ZRANGEBYSCORE', KEYS[4], '-inf', '(' .. cutoff)) end
local maxrows = tonumber(ARGV[5])
if maxrows > 0 then
  local excess = redis.call('ZCARD', KEYS[4]) - maxrows
  if excess > 0 then remove(redis.call('ZRANGE', KEYS[4], 0, excess - 1)) end
end
local maxbytes = tonumber(ARGV[6])
if maxbytes > 0 then
  while tonumber(redis.call('GET', KEYS[5]) or '0') > maxbytes do
    local oldest = redis.call('ZRANGE', KEYS[4], 0, 0)
    if #oldest == 0 then break end
    remove(oldest)
  end
end
return {id, deleted, redis.call('ZCARD', KEYS[4]), tonumber(redis.call('GET', KEYS[5]) or '0')}
`)

var retainEventScript = redis.NewScript(`
local deleted = 0
local function remove(ids)
  for _, victim in ipairs(ids) do
    local n = tonumber(redis.call('HGET', KEYS[1], victim) or '0')
    redis.call('HDEL', KEYS[1], victim)
    redis.call('HDEL', KEYS[2], victim)
    redis.call('ZREM', KEYS[3], victim)
    redis.call('DECRBY', KEYS[4], n)
    deleted = deleted + 1
  end
end
local cutoff = tonumber(ARGV[1])
if cutoff > 0 then remove(redis.call('ZRANGEBYSCORE', KEYS[3], '-inf', '(' .. cutoff)) end
local maxrows = tonumber(ARGV[2])
if maxrows > 0 then
  local excess = redis.call('ZCARD', KEYS[3]) - maxrows
  if excess > 0 then remove(redis.call('ZRANGE', KEYS[3], 0, excess - 1)) end
end
local maxbytes = tonumber(ARGV[3])
if maxbytes > 0 then
  while tonumber(redis.call('GET', KEYS[4]) or '0') > maxbytes do
    local oldest = redis.call('ZRANGE', KEYS[3], 0, 0)
    if #oldest == 0 then break end
    remove(oldest)
  end
end
return {deleted, redis.call('ZCARD', KEYS[3]), tonumber(redis.call('GET', KEYS[4]) or '0')}
`)

var clearEventScript = redis.NewScript(`
local count = redis.call('ZCARD', KEYS[1])
redis.call('DEL', KEYS[1], KEYS[2], KEYS[3], KEYS[4])
return count
`)

var saveJobEventScript = redis.NewScript(`
redis.call('RPUSH', KEYS[1], ARGV[1])
redis.call('RPUSH', KEYS[2], ARGV[2])
redis.call('INCRBY', KEYS[3], ARGV[2])
local maxRows = tonumber(ARGV[3]) or 0
local maxBytes = tonumber(ARGV[4]) or 0
while (maxRows > 0 and redis.call('LLEN', KEYS[1]) > maxRows) or
      (maxBytes > 0 and tonumber(redis.call('GET', KEYS[3]) or '0') > maxBytes) do
  redis.call('LPOP', KEYS[1])
  local removed = tonumber(redis.call('LPOP', KEYS[2]) or '0')
  redis.call('DECRBY', KEYS[3], removed)
end
local ttl = tonumber(ARGV[5]) or 0
if ttl > 0 then
  redis.call('EXPIRE', KEYS[1], ttl)
  redis.call('EXPIRE', KEYS[2], ttl)
  redis.call('EXPIRE', KEYS[3], ttl)
end
return redis.call('LLEN', KEYS[1])
`)

func (r *Redis) jobEventKeys(jobID string) []string {
	return []string{
		r.key("job-logs", jobID, "data"),
		r.key("job-logs", jobID, "size"),
		r.key("job-logs", jobID, "bytes"),
	}
}

func (r *Redis) SaveJobEvent(jobID string, value domain.Event, retention JobEventRetention) error {
	if err := r.ready(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode job event: %w", err)
	}
	ttl := int64(retention.TTL / time.Second)
	if retention.TTL > 0 && ttl < 1 {
		ttl = 1
	}
	_, err = saveJobEventScript.Run(
		context.Background(), r.client, r.jobEventKeys(jobID),
		string(data), len(data), retention.MaxRows, retention.MaxBytes, ttl,
	).Result()
	if err != nil {
		return fmt.Errorf("save job event: %w", err)
	}
	return nil
}

func (r *Redis) ListJobEvents(jobID string, after uint64) ([]domain.Event, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	raw, err := r.client.LRange(context.Background(), r.jobEventKeys(jobID)[0], 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("list job events: %w", err)
	}
	values := make([]domain.Event, 0, len(raw))
	for _, item := range raw {
		var value domain.Event
		if err := json.Unmarshal([]byte(item), &value); err != nil {
			return nil, fmt.Errorf("decode job event: %w", err)
		}
		if value.ID > after {
			values = append(values, value)
		}
	}
	return values, nil
}

func (r *Redis) eventKeys() []string {
	return []string{r.key("events", "seq"), r.key("events", "data"), r.key("events", "size"), r.key("events", "index"), r.key("events", "bytes")}
}

func retentionArgs(value EventRetention) (int64, int, int64) {
	now := value.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var cutoff int64
	if value.MaxAge > 0 {
		cutoff = now.Add(-value.MaxAge).UnixNano()
	}
	return cutoff, value.MaxRows, value.MaxBytes
}

func (r *Redis) SaveEvent(value Event, retention ...EventRetention) error {
	if err := r.ready(); err != nil {
		return err
	}
	value, data, err := prepareEvent(value)
	if err != nil {
		return err
	}
	value.Data = nil
	body, err := json.Marshal(struct {
		Event
		Data json.RawMessage `json:"data,omitempty"`
	}{Event: value, Data: data})
	if err != nil {
		return err
	}
	policy := EventRetention{}
	if len(retention) > 0 {
		policy = retention[0]
	}
	cutoff, rows, bytesLimit := retentionArgs(policy)
	_, err = saveEventScript.Run(context.Background(), r.client, r.eventKeys(), body, eventSize(value, data), value.At.UnixNano(), cutoff, rows, bytesLimit).Result()
	if err != nil {
		return fmt.Errorf("save event: %w", err)
	}
	return nil
}

func eventMatches(value Event, filter EventFilter) bool {
	scheduleID := value.ScheduleID
	if scheduleID == "" {
		scheduleID, _ = value.Data["scheduleId"].(string)
	}
	return (filter.ProviderID == "" || value.ProviderID == filter.ProviderID) &&
		(filter.JobID == "" || value.JobID == filter.JobID) &&
		(filter.ScheduleID == "" || scheduleID == filter.ScheduleID) &&
		(filter.Type == "" || value.Type == filter.Type) &&
		(filter.Level == "" || value.Level == filter.Level) &&
		(filter.Since.IsZero() || !value.At.Before(filter.Since)) &&
		(filter.Until.IsZero() || !value.At.After(filter.Until))
}

func eventScoreBounds(filter EventFilter) (string, string) {
	minScore, maxScore := "-inf", "+inf"
	if !filter.Since.IsZero() {
		minScore = strconv.FormatInt(filter.Since.UnixNano(), 10)
	}
	if !filter.Until.IsZero() {
		maxScore = strconv.FormatInt(filter.Until.UnixNano(), 10)
	}
	return minScore, maxScore
}

func (r *Redis) eventBatch(ctx context.Context, filter EventFilter, offset, count int64) ([]Event, error) {
	minScore, maxScore := eventScoreBounds(filter)
	ids, err := r.client.ZRevRangeByScore(ctx, r.key("events", "index"), &redis.ZRangeBy{
		Min: minScore, Max: maxScore, Offset: offset, Count: count,
	}).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	raw, err := r.client.HMGet(ctx, r.key("events", "data"), ids...).Result()
	if err != nil {
		return nil, err
	}
	values := make([]Event, 0, len(ids))
	for index, item := range raw {
		if item == nil {
			continue
		}
		var value Event
		if err = json.Unmarshal([]byte(fmt.Sprint(item)), &value); err != nil {
			return nil, fmt.Errorf("decode event: %w", err)
		}
		value.ID, _ = strconv.ParseInt(ids[index], 10, 64)
		values = append(values, value)
	}
	return values, nil
}

func (r *Redis) ListEvents(filter EventFilter) ([]Event, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	offset := int64(max(0, filter.Offset))
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 1000)
	ctx := context.Background()
	result := make([]Event, 0, limit)
	var candidateOffset, matched int64
	const batchSize int64 = 256
	for len(result) < limit {
		values, err := r.eventBatch(ctx, filter, candidateOffset, batchSize)
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			break
		}
		candidateOffset += int64(len(values))
		for _, value := range values {
			if !eventMatches(value, filter) {
				continue
			}
			if matched < offset {
				matched++
				continue
			}
			result = append(result, value)
			if len(result) == limit {
				break
			}
		}
		if len(values) < int(batchSize) {
			break
		}
	}
	return result, nil
}

func (r *Redis) CountEvents(filter EventFilter) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	ctx := context.Background()
	minScore, maxScore := eventScoreBounds(filter)
	if filter.ProviderID == "" && filter.JobID == "" && filter.Type == "" && filter.Level == "" {
		return r.client.ZCount(ctx, r.key("events", "index"), minScore, maxScore).Result()
	}
	var count int64
	var candidateOffset int64
	const batchSize int64 = 512
	for {
		values, err := r.eventBatch(ctx, filter, candidateOffset, batchSize)
		if err != nil {
			return 0, err
		}
		if len(values) == 0 {
			break
		}
		candidateOffset += int64(len(values))
		for _, value := range values {
			if eventMatches(value, filter) {
				count++
			}
		}
		if len(values) < int(batchSize) {
			break
		}
	}
	return count, nil
}

func (r *Redis) ClearEvents() (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	keys := []string{r.key("events", "index"), r.key("events", "data"), r.key("events", "size"), r.key("events", "bytes")}
	count, err := clearEventScript.Run(context.Background(), r.client, keys).Int64()
	return count, err
}

// DeleteEventsByType removes legacy event records that must no longer be
// retained in the long-lived operational event ledger.
func (r *Redis) DeleteEventsByType(eventType string) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return 0, errors.New("event type is required")
	}
	ctx := context.Background()
	raw, err := r.client.HGetAll(ctx, r.key("events", "data")).Result()
	if err != nil {
		return 0, fmt.Errorf("list events for type cleanup: %w", err)
	}
	ids := make([]string, 0)
	for id, body := range raw {
		var value Event
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			return 0, fmt.Errorf("decode event %s for type cleanup: %w", id, err)
		}
		if value.Type == eventType {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var removedBytes int64
	for _, id := range ids {
		size, sizeErr := r.client.HGet(ctx, r.key("events", "size"), id).Int64()
		if sizeErr == nil && size > 0 {
			removedBytes += size
		}
	}
	_, err = r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HDel(ctx, r.key("events", "data"), ids...)
		pipe.HDel(ctx, r.key("events", "size"), ids...)
		members := make([]any, len(ids))
		for index, id := range ids {
			members[index] = id
		}
		pipe.ZRem(ctx, r.key("events", "index"), members...)
		if removedBytes > 0 {
			pipe.DecrBy(ctx, r.key("events", "bytes"), removedBytes)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("delete events by type: %w", err)
	}
	if current, getErr := r.client.Get(ctx, r.key("events", "bytes")).Int64(); getErr == nil && current < 0 {
		_ = r.client.Set(ctx, r.key("events", "bytes"), 0, 0).Err()
	}
	return int64(len(ids)), nil
}

func (r *Redis) RetainEvents(value EventRetention) (RetentionResult, error) {
	if err := r.ready(); err != nil {
		return RetentionResult{}, err
	}
	cutoff, rows, bytesLimit := retentionArgs(value)
	keys := []string{r.key("events", "size"), r.key("events", "data"), r.key("events", "index"), r.key("events", "bytes")}
	raw, err := retainEventScript.Run(context.Background(), r.client, keys, cutoff, rows, bytesLimit).Slice()
	if err != nil || len(raw) != 3 {
		return RetentionResult{}, fmt.Errorf("retain events: %w", err)
	}
	return RetentionResult{Deleted: toInt64(raw[0]), Count: toInt64(raw[1]), Bytes: toInt64(raw[2])}, nil
}

func (r *Redis) Diagnostics() (Diagnostics, error) {
	if err := r.ready(); err != nil {
		return Diagnostics{}, err
	}
	ctx := context.Background()
	count, err := r.client.ZCard(ctx, r.key("events", "index")).Result()
	if err != nil {
		return Diagnostics{}, err
	}
	bytesValue, _ := r.client.Get(ctx, r.key("events", "bytes")).Int64()
	schedules, err := r.client.HLen(ctx, r.key("schedules")).Result()
	if err != nil {
		return Diagnostics{}, err
	}
	version, err := r.client.Get(ctx, r.key("schema", "version")).Int()
	return Diagnostics{Backend: "redis", SchemaVersion: version, LogicalBytes: bytesValue, EventCount: count, ScheduleCount: schedules}, err
}

type encryptedValue struct {
	Version    int    `json:"version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

const ccSwitchSnapshotSentinel = "__ai_watch_snapshot__"

type ccSwitchProviderRecord struct {
	Version           int            `json:"version"`
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	CLI               domain.CLI     `json:"cli"`
	Current           bool           `json:"current"`
	BaseURL           string         `json:"baseUrl,omitempty"`
	Model             string         `json:"model,omitempty"`
	Provider          string         `json:"provider,omitempty"`
	UpdatedAt         time.Time      `json:"updatedAt"`
	APIKeySecret      encryptedValue `json:"apiKeySecret"`
	CodexConfigSecret encryptedValue `json:"codexConfigSecret"`
	ClaudeEnvSecret   encryptedValue `json:"claudeEnvSecret"`
}

// CCSwitchSyncStatus describes the latest startup import attempt. A failed
// attempt changes only this status; the last successfully imported provider
// snapshot remains available for runtime resolution.
type CCSwitchSyncStatus struct {
	SourceAvailable bool       `json:"sourceAvailable"`
	LastAttemptAt   time.Time  `json:"lastAttemptAt"`
	LastSuccessAt   *time.Time `json:"lastSuccessAt,omitempty"`
	Count           int        `json:"count"`
	Warning         string     `json:"warning,omitempty"`
}

func (r *Redis) ListCCSwitchProviders() ([]domain.CCSwitchProvider, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneCCSwitchProviders(r.ccSwitch), nil
}

func (r *Redis) GetCCSwitchProvider(id string) (domain.CCSwitchProvider, error) {
	if err := r.ready(); err != nil {
		return domain.CCSwitchProvider{}, err
	}
	id = strings.TrimSpace(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, value := range r.ccSwitch {
		if value.ID == id {
			return cloneCCSwitchProvider(value), nil
		}
	}
	return domain.CCSwitchProvider{}, fs.ErrNotExist
}

func (r *Redis) ReplaceCCSwitchProviders(values []domain.CCSwitchProvider) error {
	if err := r.ready(); err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	encoded := make(map[string]any, len(values)+1)
	encoded[ccSwitchSnapshotSentinel] = redisSchemaVersion
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.ID = strings.TrimSpace(value.ID)
		if value.ID == "" {
			return errors.New("CC Switch provider id is required")
		}
		if _, exists := seen[value.ID]; exists {
			return fmt.Errorf("duplicate CC Switch provider id %q", value.ID)
		}
		seen[value.ID] = struct{}{}
		record, err := r.encryptCCSwitchProvider(value)
		if err != nil {
			return fmt.Errorf("encrypt CC Switch provider %q: %w", value.ID, err)
		}
		body, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode CC Switch provider %q: %w", value.ID, err)
		}
		encoded["provider:"+value.ID] = body
	}

	ctx := context.Background()
	temporaryKey := r.key("cc-switch-providers", "tmp", randomHex(12))
	defer r.client.Del(context.Background(), temporaryKey)
	if err := r.client.HSet(ctx, temporaryKey, encoded).Err(); err != nil {
		return fmt.Errorf("write temporary CC Switch provider snapshot: %w", err)
	}
	if err := r.client.Rename(ctx, temporaryKey, r.key("cc-switch-providers")).Err(); err != nil {
		return fmt.Errorf("replace CC Switch provider snapshot: %w", err)
	}

	cached := cloneCCSwitchProviders(values)
	sortCCSwitchProviders(cached)
	r.mu.Lock()
	r.ccSwitch = cached
	r.mu.Unlock()
	return nil
}

func (r *Redis) encryptCCSwitchProvider(value domain.CCSwitchProvider) (ccSwitchProviderRecord, error) {
	apiKey, err := r.encrypt(value.APIKey)
	if err != nil {
		return ccSwitchProviderRecord{}, err
	}
	codexConfig, err := r.encrypt(value.CodexConfig)
	if err != nil {
		return ccSwitchProviderRecord{}, err
	}
	claudeEnvJSON, err := json.Marshal(value.ClaudeEnv)
	if err != nil {
		return ccSwitchProviderRecord{}, errors.New("encode Claude environment")
	}
	claudeEnv, err := r.encrypt(string(claudeEnvJSON))
	if err != nil {
		return ccSwitchProviderRecord{}, err
	}
	return ccSwitchProviderRecord{
		Version: 1, ID: value.ID, Name: value.Name, CLI: value.CLI,
		Current: value.Current, BaseURL: value.BaseURL, Model: value.Model,
		Provider: value.Provider, UpdatedAt: value.UpdatedAt,
		APIKeySecret: apiKey, CodexConfigSecret: codexConfig, ClaudeEnvSecret: claudeEnv,
	}, nil
}

func (r *Redis) decryptCCSwitchProvider(record ccSwitchProviderRecord) (domain.CCSwitchProvider, error) {
	if record.Version != 1 {
		return domain.CCSwitchProvider{}, errors.New("unsupported encrypted CC Switch provider record version")
	}
	apiKey, err := r.decrypt(record.APIKeySecret)
	if err != nil {
		return domain.CCSwitchProvider{}, err
	}
	codexConfig, err := r.decrypt(record.CodexConfigSecret)
	if err != nil {
		return domain.CCSwitchProvider{}, err
	}
	claudeEnvJSON, err := r.decrypt(record.ClaudeEnvSecret)
	if err != nil {
		return domain.CCSwitchProvider{}, err
	}
	var claudeEnv map[string]string
	if err = json.Unmarshal([]byte(claudeEnvJSON), &claudeEnv); err != nil {
		return domain.CCSwitchProvider{}, errors.New("decode encrypted Claude environment")
	}
	return domain.CCSwitchProvider{
		ID: record.ID, Name: record.Name, CLI: record.CLI, Current: record.Current,
		BaseURL: record.BaseURL, Model: record.Model, Provider: record.Provider,
		APIKey: apiKey, CodexConfig: codexConfig, ClaudeEnv: claudeEnv,
		UpdatedAt: record.UpdatedAt,
	}, nil
}

func (r *Redis) listCCSwitchProvidersRedis(ctx context.Context) ([]domain.CCSwitchProvider, error) {
	raw, err := r.client.HGetAll(ctx, r.key("cc-switch-providers")).Result()
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return []domain.CCSwitchProvider{}, nil
	}
	if _, ok := raw[ccSwitchSnapshotSentinel]; !ok {
		return nil, errors.New("CC Switch provider snapshot is incomplete")
	}
	result := make([]domain.CCSwitchProvider, 0, len(raw)-1)
	for field, body := range raw {
		if field == ccSwitchSnapshotSentinel {
			continue
		}
		if !strings.HasPrefix(field, "provider:") {
			return nil, errors.New("CC Switch provider snapshot contains an unknown field")
		}
		var record ccSwitchProviderRecord
		if err = json.Unmarshal([]byte(body), &record); err != nil {
			return nil, errors.New("decode encrypted CC Switch provider record")
		}
		value, decryptErr := r.decryptCCSwitchProvider(record)
		if decryptErr != nil {
			return nil, fmt.Errorf("decrypt CC Switch provider %q: %w", record.ID, decryptErr)
		}
		result = append(result, value)
	}
	sortCCSwitchProviders(result)
	return result, nil
}

func sortCCSwitchProviders(values []domain.CCSwitchProvider) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].CLI != values[j].CLI {
			return values[i].CLI == domain.CLICodex
		}
		if values[i].Current != values[j].Current {
			return values[i].Current
		}
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		return values[i].ID < values[j].ID
	})
}

func cloneCCSwitchProvider(value domain.CCSwitchProvider) domain.CCSwitchProvider {
	if value.ClaudeEnv != nil {
		source := value.ClaudeEnv
		value.ClaudeEnv = make(map[string]string, len(source))
		for key, item := range source {
			value.ClaudeEnv[key] = item
		}
	}
	return value
}

func cloneCCSwitchProviders(values []domain.CCSwitchProvider) []domain.CCSwitchProvider {
	result := make([]domain.CCSwitchProvider, len(values))
	for index := range values {
		result[index] = cloneCCSwitchProvider(values[index])
	}
	return result
}

func (r *Redis) LoadCCSwitchSyncStatus() (CCSwitchSyncStatus, error) {
	if err := r.ready(); err != nil {
		return CCSwitchSyncStatus{}, err
	}
	var value CCSwitchSyncStatus
	err := r.hgetStringJSON(context.Background(), r.key("cc-switch-sync-status"), &value)
	if errors.Is(err, redis.Nil) {
		return CCSwitchSyncStatus{}, nil
	}
	if err != nil {
		return CCSwitchSyncStatus{}, fmt.Errorf("load CC Switch sync status: %w", err)
	}
	return value, nil
}

func (r *Redis) SaveCCSwitchSyncStatus(value CCSwitchSyncStatus) error {
	if err := r.ready(); err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode CC Switch sync status: %w", err)
	}
	if err = r.client.Set(context.Background(), r.key("cc-switch-sync-status"), body, 0).Err(); err != nil {
		return fmt.Errorf("save CC Switch sync status: %w", err)
	}
	return nil
}

func (r *Redis) hgetStringJSON(ctx context.Context, key string, target any) error {
	body, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

type manualProviderRecord struct {
	domain.ManualProvider
	Version     int            `json:"version"`
	Secret      encryptedValue `json:"secret"`
	ProxySecret encryptedValue `json:"proxySecret,omitempty"`
}

func (r *Redis) ListManualProviders() ([]domain.ManualProvider, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]domain.ManualProvider, len(r.manual))
	copy(result, r.manual)
	return result, nil
}

func (r *Redis) listManualProvidersRedis(ctx context.Context) ([]domain.ManualProvider, error) {
	values, err := r.client.HVals(ctx, r.key("manual-providers")).Result()
	if err != nil {
		return nil, err
	}
	result := make([]domain.ManualProvider, 0, len(values))
	for _, raw := range values {
		var record manualProviderRecord
		if err = json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, errors.New("decode encrypted provider record")
		}
		if record.Version == 0 {
			record.Enabled = true
		}
		if record.ProxyMode == "" {
			record.ProxyMode = domain.ProxyDefault
		}
		if record.Secret.Ciphertext != "" {
			record.APIKey, err = r.decrypt(record.Secret)
			if err != nil {
				return nil, err
			}
			record.HasAPIKey = true
			record.MaskedKey = security.Mask(record.APIKey)
		}
		if record.ProxySecret.Ciphertext != "" {
			record.ProxyURL, err = r.decrypt(record.ProxySecret)
			if err != nil {
				return nil, err
			}
			record.HasProxyURL = true
			record.MaskedProxyURL = maskStoredProxyURL(record.ProxyURL)
		}
		result = append(result, record.ManualProvider)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (r *Redis) GetManualProvider(id string) (domain.ManualProvider, error) {
	if err := r.ready(); err != nil {
		return domain.ManualProvider{}, err
	}
	id = strings.TrimSpace(id)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, value := range r.manual {
		if value.ID == id {
			return value, nil
		}
	}
	return domain.ManualProvider{}, fs.ErrNotExist
}

func (r *Redis) UpsertManualProvider(value domain.ManualProvider) (domain.ManualProvider, error) {
	if err := r.ready(); err != nil {
		return domain.ManualProvider{}, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	now := time.Now().UTC()
	if value.ID == "" {
		value.ID = "manual-" + randomHex(8)
	}
	if existing, err := r.GetManualProvider(value.ID); err == nil {
		value.CreatedAt = existing.CreatedAt
		if value.APIKey == "" && !value.ClearAPIKey {
			value.APIKey = existing.APIKey
		}
		if value.ProxyURL == "" && !value.ClearProxyURL {
			value.ProxyURL = existing.ProxyURL
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		value.CreatedAt = now
	} else {
		return domain.ManualProvider{}, err
	}
	value.UpdatedAt = now
	record := manualProviderRecord{Version: 1, ManualProvider: value}
	record.ManualProvider.APIKey = ""
	record.ManualProvider.ProxyURL = ""
	record.ManualProvider.ClearAPIKey = false
	record.ManualProvider.ClearProxyURL = false
	record.ManualProvider.HasAPIKey = false
	record.ManualProvider.MaskedKey = ""
	record.ManualProvider.HasProxyURL = false
	record.ManualProvider.MaskedProxyURL = ""
	if value.APIKey != "" {
		secret, err := r.encrypt(value.APIKey)
		if err != nil {
			return domain.ManualProvider{}, err
		}
		record.Secret = secret
		value.HasAPIKey = true
		value.MaskedKey = security.Mask(value.APIKey)
	} else {
		value.HasAPIKey = false
		value.MaskedKey = ""
	}
	if value.ProxyURL != "" {
		secret, err := r.encrypt(value.ProxyURL)
		if err != nil {
			return domain.ManualProvider{}, err
		}
		record.ProxySecret = secret
		value.HasProxyURL = true
		value.MaskedProxyURL = maskStoredProxyURL(value.ProxyURL)
	} else {
		value.HasProxyURL = false
		value.MaskedProxyURL = ""
	}
	value.ClearAPIKey = false
	value.ClearProxyURL = false
	if err := r.hsetJSON(context.Background(), r.key("manual-providers"), value.ID, record); err != nil {
		return domain.ManualProvider{}, err
	}
	r.cacheManualProvider(value)
	return value, nil
}

func (r *Redis) DeleteManualProvider(id string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	id = strings.TrimSpace(id)
	deleted, err := r.client.HDel(context.Background(), r.key("manual-providers"), strings.TrimSpace(id)).Result()
	if err == nil && deleted > 0 {
		r.removeCachedManualProvider(id)
	}
	return deleted > 0, err
}

type dingTalkRecord struct {
	Source    string         `json:"source"`
	UpdatedAt *time.Time     `json:"updatedAt,omitempty"`
	Secret    encryptedValue `json:"secret"`
}

func (r *Redis) LoadDingTalkConfig() (domain.DingTalkConfig, error) {
	if err := r.ready(); err != nil {
		return domain.DingTalkConfig{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dingTalk, nil
}

func (r *Redis) loadDingTalkConfigRedis(ctx context.Context) (domain.DingTalkConfig, error) {
	raw, err := r.client.Get(ctx, r.key("dingtalk")).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.DingTalkConfig{}, nil
	}
	if err != nil {
		return domain.DingTalkConfig{}, err
	}
	var record dingTalkRecord
	if json.Unmarshal(raw, &record) != nil {
		return domain.DingTalkConfig{}, errors.New("decode encrypted DingTalk record")
	}
	webhook, err := r.decrypt(record.Secret)
	if err != nil {
		return domain.DingTalkConfig{}, err
	}
	return domain.DingTalkConfig{WebhookURL: webhook, Configured: webhook != "", Source: record.Source, MaskedWebhook: security.Mask(webhook), UpdatedAt: record.UpdatedAt}, nil
}

func (r *Redis) SaveDingTalkConfig(value domain.DingTalkConfig) (domain.DingTalkConfig, error) {
	if err := r.ready(); err != nil {
		return domain.DingTalkConfig{}, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	secret, err := r.encrypt(value.WebhookURL)
	if err != nil {
		return domain.DingTalkConfig{}, err
	}
	now := time.Now().UTC()
	record := dingTalkRecord{Source: value.Source, UpdatedAt: &now, Secret: secret}
	b, _ := json.Marshal(record)
	if err = r.client.Set(context.Background(), r.key("dingtalk"), b, 0).Err(); err != nil {
		return domain.DingTalkConfig{}, err
	}
	value.Configured, value.MaskedWebhook, value.UpdatedAt = value.WebhookURL != "", security.Mask(value.WebhookURL), &now
	r.mu.Lock()
	r.dingTalk = value
	r.mu.Unlock()
	return value, nil
}

func (r *Redis) ClearDingTalkConfig() (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	deleted, err := r.client.Del(context.Background(), r.key("dingtalk")).Result()
	if err == nil {
		r.mu.Lock()
		r.dingTalk = domain.DingTalkConfig{}
		r.mu.Unlock()
	}
	return deleted > 0, err
}

func (r *Redis) encrypt(value string) (encryptedValue, error) {
	if r.aead == nil {
		return encryptedValue{}, errors.New("encryption key is unavailable")
	}
	nonce := make([]byte, r.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return encryptedValue{}, errors.New("generate encryption nonce")
	}
	ciphertext := r.aead.Seal(nil, nonce, []byte(value), []byte(r.prefix))
	return encryptedValue{Version: 1, Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext)}, nil
}

func (r *Redis) decrypt(value encryptedValue) (string, error) {
	if r.aead == nil {
		return "", errors.New("encryption key is unavailable")
	}
	if value.Version != 0 && value.Version != 1 {
		return "", errors.New("unsupported encrypted secret version")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(value.Nonce)
	if err != nil {
		return "", errors.New("decrypt stored secret")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(value.Ciphertext)
	if err != nil {
		return "", errors.New("decrypt stored secret")
	}
	plaintext, err := r.aead.Open(nil, nonce, ciphertext, []byte(r.prefix))
	if err != nil {
		return "", errors.New("decrypt stored secret")
	}
	return string(plaintext), nil
}

func (r *Redis) cacheManualProvider(value domain.ManualProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for index := range r.manual {
		if r.manual[index].ID == value.ID {
			r.manual[index] = value
			found = true
			break
		}
	}
	if !found {
		r.manual = append(r.manual, value)
	}
	sort.Slice(r.manual, func(i, j int) bool {
		if r.manual[i].Name != r.manual[j].Name {
			return r.manual[i].Name < r.manual[j].Name
		}
		return r.manual[i].ID < r.manual[j].ID
	})
}

func (r *Redis) removeCachedManualProvider(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range r.manual {
		if r.manual[index].ID == id {
			r.manual = append(r.manual[:index], r.manual[index+1:]...)
			return
		}
	}
}

func maskStoredProxyURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "configured"
	}
	masked := parsed.Scheme + "://"
	if parsed.User != nil {
		masked += "****@"
	}
	return masked + parsed.Host
}

func loadEncryptionKey(dataDir, configured string) ([]byte, error) {
	if strings.TrimSpace(configured) != "" {
		key, err := decodeEncryptionKey(configured)
		if err != nil {
			return nil, errors.New("AI_WATCH_MASTER_KEY must decode to 32 bytes")
		}
		return key, nil
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data directory for master key: %w", err)
	}
	path := filepath.Join(dataDir, "master.key")
	if raw, err := os.ReadFile(path); err == nil {
		key, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("stored master key is invalid")
		}
		if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
			return nil, fmt.Errorf("protect master key: %w", chmodErr)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, errors.New("generate master key")
	}
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}

func decodeEncryptionKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, decode := range []func(string) ([]byte, error){base64.RawStdEncoding.DecodeString, base64.StdEncoding.DecodeString, hex.DecodeString} {
		if key, err := decode(value); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, errors.New("invalid encryption key")
}

func (r *Redis) seedDefaults(ctx context.Context) error {
	if exists, err := r.client.Exists(ctx, r.key("settings")).Result(); err != nil {
		return err
	} else if exists == 0 {
		b, _ := json.Marshal(domain.DefaultSettings())
		if err = r.client.Set(ctx, r.key("settings"), b, 0).Err(); err != nil {
			return err
		}
	}
	count, err := r.client.HLen(ctx, r.key("provider-examples")).Result()
	if err != nil {
		return err
	}
	if count == 0 {
		now := time.Now().UTC()
		for i, value := range defaultProviderExamples() {
			value.UpdatedAt = now.Add(time.Duration(i) * time.Nanosecond)
			if err = r.hsetJSON(ctx, r.key("provider-examples"), value.ID, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Redis) migrateSQLite(ctx context.Context) error {
	marker := r.key("migration", "sqlite-v1")
	if ok, err := r.client.Exists(ctx, marker).Result(); err != nil || ok > 0 {
		return err
	}
	dbPath := filepath.Join(r.dataDir, databaseName)
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return r.client.Set(ctx, marker, "no-source", 0).Err()
	} else if err != nil {
		return fmt.Errorf("inspect SQLite migration source: %w", err)
	}
	snapshotDir, cleanup, err := snapshotSQLiteMigrationSource(r.dataDir)
	if err != nil {
		return err
	}
	defer cleanup()
	legacy := NewReadOnly(snapshotDir)
	if err := legacy.Err(); err != nil {
		return fmt.Errorf("open SQLite migration source: %w", err)
	}
	defer legacy.Close()
	settings, err := legacy.LoadSettings()
	if err != nil {
		return err
	}
	b, _ := json.Marshal(settings)
	if err = r.client.Set(ctx, r.key("settings"), b, 0).Err(); err != nil {
		return err
	}
	if values, readErr := legacy.ListProviderExamples(); readErr != nil {
		return readErr
	} else {
		for _, value := range values {
			if err = r.hsetJSON(ctx, r.key("provider-examples"), value.ID, value); err != nil {
				return err
			}
		}
	}
	if values, readErr := legacy.ListSchedules(); readErr != nil {
		return readErr
	} else {
		for _, value := range values {
			if err = r.hsetJSON(ctx, r.key("schedules"), value.ID, value); err != nil {
				return err
			}
		}
	}
	if values, readErr := legacy.LoadSummaries(); readErr != nil {
		return readErr
	} else {
		// SQLite returns newest first; Redis assigns increasing sequence scores
		// and reads them in reverse, so import oldest first to preserve order.
		for index := len(values) - 1; index >= 0; index-- {
			value := values[index]
			b, _ = json.Marshal(value)
			seq, seqErr := r.client.Incr(ctx, r.key("summaries", "seq")).Result()
			if seqErr != nil {
				return seqErr
			}
			if _, err = r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, r.key("summaries", "data"), value.ID, b)
				pipe.ZAdd(ctx, r.key("summaries", "index"), redis.Z{Score: float64(seq), Member: value.ID})
				return nil
			}); err != nil {
				return err
			}
		}
	}
	for offset := 0; ; offset += 1000 {
		values, readErr := legacy.ListEvents(EventFilter{Limit: 1000, Offset: offset})
		if readErr != nil {
			return readErr
		}
		if len(values) == 0 {
			break
		}
		for _, value := range values {
			prepared, data, prepareErr := prepareEvent(value)
			if prepareErr != nil {
				return prepareErr
			}
			prepared.Data = nil
			body, _ := json.Marshal(struct {
				Event
				Data json.RawMessage `json:"data,omitempty"`
			}{Event: prepared, Data: data})
			id := strconv.FormatInt(value.ID, 10)
			size := eventSize(prepared, data)
			if _, err = r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.HSet(ctx, r.key("events", "data"), id, body)
				pipe.HSet(ctx, r.key("events", "size"), id, size)
				pipe.ZAdd(ctx, r.key("events", "index"), redis.Z{Score: float64(value.At.UnixNano()), Member: id})
				return nil
			}); err != nil {
				return err
			}
		}
		if len(values) < 1000 {
			break
		}
	}
	ids, _ := r.client.HKeys(ctx, r.key("events", "size")).Result()
	var total, maxID int64
	if len(ids) > 0 {
		raw, _ := r.client.HMGet(ctx, r.key("events", "size"), ids...).Result()
		for i, item := range raw {
			total += toInt64(item)
			id, _ := strconv.ParseInt(ids[i], 10, 64)
			if id > maxID {
				maxID = id
			}
		}
	}
	pipe := r.client.TxPipeline()
	pipe.Set(ctx, r.key("events", "bytes"), total, 0)
	pipe.Set(ctx, r.key("events", "seq"), maxID, 0)
	pipe.Set(ctx, marker, time.Now().UTC().Format(time.RFC3339Nano), 0)
	_, err = pipe.Exec(ctx)
	return err
}

func snapshotSQLiteMigrationSource(dataDir string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ai-watch-sqlite-migration-")
	if err != nil {
		return "", nil, fmt.Errorf("create SQLite migration snapshot: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source := filepath.Join(dataDir, databaseName) + suffix
		input, openErr := os.Open(source)
		if errors.Is(openErr, os.ErrNotExist) && suffix != "" {
			continue
		}
		if openErr != nil {
			cleanup()
			return "", nil, fmt.Errorf("open SQLite migration source: %w", openErr)
		}
		target := filepath.Join(dir, databaseName) + suffix
		output, createErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			_ = input.Close()
			cleanup()
			return "", nil, fmt.Errorf("create SQLite migration snapshot: %w", createErr)
		}
		_, copyErr := io.Copy(output, input)
		closeOutErr := output.Close()
		closeInErr := input.Close()
		if copyErr != nil || closeOutErr != nil || closeInErr != nil {
			cleanup()
			return "", nil, fmt.Errorf("copy SQLite migration snapshot: %v", errors.Join(copyErr, closeOutErr, closeInErr))
		}
	}
	return dir, cleanup, nil
}

func (r *Redis) hsetJSON(ctx context.Context, key, field string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.client.HSet(ctx, key, field, b).Err()
}

func (r *Redis) hgetJSON(ctx context.Context, key, field string, target any) error {
	b, err := r.client.HGet(ctx, key, field).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func (r *Redis) hvalsJSON(ctx context.Context, key string, target any) error {
	values, err := r.client.HVals(ctx, key).Result()
	if err != nil {
		return err
	}
	b, err := json.Marshal(values)
	if err != nil {
		return err
	}
	var raw []string
	if err = json.Unmarshal(b, &raw); err != nil {
		return err
	}
	joined := "[" + strings.Join(raw, ",") + "]"
	return json.Unmarshal([]byte(joined), target)
}

func stringSliceAny(values []string) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

func toInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case string:
		v, _ := strconv.ParseInt(typed, 10, 64)
		return v
	default:
		v, _ := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		return v
	}
}

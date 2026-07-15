package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ai-watch/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	redisDefaultLimit = 50
	redisMaxLimit     = 200
	redisMaxKeyLength = 4096
	redisMaxPattern   = 512
	redisMaxValueSize = 256 << 10
	redisMaxTTL       = int64(10 * 365 * 24 * 60 * 60)
)

type redisKeySummary struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	TTLMillis   int64  `json:"ttlMillis"`
	Persistent  bool   `json:"persistent"`
	Size        int64  `json:"size"`
	MemoryBytes int64  `json:"memoryBytes,omitempty"`
}

type redisKeyDetail struct {
	redisKeySummary
	Encoding   string `json:"encoding,omitempty"`
	Version    string `json:"version"`
	Cursor     string `json:"cursor,omitempty"`
	NextCursor string `json:"nextCursor,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Value      any    `json:"value,omitempty"`
}

type redisHashEntry struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type redisZSetEntry struct {
	Member string  `json:"member"`
	Score  float64 `json:"score"`
}

type redisMutationInput struct {
	Key        string   `json:"key"`
	Operation  string   `json:"operation"`
	Version    string   `json:"version"`
	ConfirmKey string   `json:"confirmKey,omitempty"`
	Value      string   `json:"value,omitempty"`
	Field      string   `json:"field,omitempty"`
	Member     string   `json:"member,omitempty"`
	Score      *float64 `json:"score,omitempty"`
	Index      *int64   `json:"index,omitempty"`
}

type redisTTLInput struct {
	Key        string `json:"key"`
	Version    string `json:"version"`
	ConfirmKey string `json:"confirmKey"`
	TTLSeconds *int64 `json:"ttlSeconds"`
}

type redisRenameInput struct {
	Key        string `json:"key"`
	NewKey     string `json:"newKey"`
	Version    string `json:"version"`
	ConfirmKey string `json:"confirmKey"`
}

type redisDeleteInput struct {
	Key        string `json:"key"`
	Version    string `json:"version"`
	ConfirmKey string `json:"confirmKey"`
}

type redisBackup struct {
	key      string
	typeName string
	ttl      time.Duration
	stringV  string
	hashV    map[string]string
	listV    []string
	setV     []string
	zsetV    []redis.Z
}

func (s *Server) redisRoute(w http.ResponseWriter, r *http.Request) {
	if s.redis == nil || s.redis.AdminClient() == nil {
		writeError(w, http.StatusServiceUnavailable, "redis_unavailable", "Redis management is unavailable")
		return
	}
	switch {
	case r.URL.Path == "/api/redis/overview" && r.Method == http.MethodGet:
		s.redisOverview(w, r)
	case r.URL.Path == "/api/redis/keys" && r.Method == http.MethodGet:
		s.redisKeys(w, r)
	case r.URL.Path == "/api/redis/keys/detail" && r.Method == http.MethodGet:
		s.redisKeyDetail(w, r)
	case r.URL.Path == "/api/redis/keys/value" && r.Method == http.MethodPut:
		s.redisMutateValue(w, r)
	case r.URL.Path == "/api/redis/keys/ttl" && r.Method == http.MethodPut:
		s.redisUpdateTTL(w, r)
	case r.URL.Path == "/api/redis/keys/rename" && r.Method == http.MethodPost:
		s.redisRename(w, r)
	case r.URL.Path == "/api/redis/keys" && r.Method == http.MethodDelete:
		s.redisDelete(w, r)
	case r.URL.Path == "/api/redis/prewarm" && r.Method == http.MethodPost:
		s.redisPrewarm(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "Redis API endpoint not found")
	}
}

func (s *Server) redisOverview(w http.ResponseWriter, r *http.Request) {
	client := s.redis.AdminClient()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if err := client.Ping(ctx).Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis_unavailable", err.Error())
		return
	}
	keyCount, err := client.DBSize(ctx).Result()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis_unavailable", err.Error())
		return
	}
	values := parseRedisInfo("")
	if info, infoErr := client.Info(ctx, "server", "memory", "stats", "clients", "keyspace").Result(); infoErr == nil {
		values = parseRedisInfo(info)
	}
	hits := parseInt64(values["keyspace_hits"])
	misses := parseInt64(values["keyspace_misses"])
	hitRate := float64(0)
	if hits+misses > 0 {
		hitRate = float64(hits) / float64(hits+misses)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected":        true,
		"version":          values["redis_version"],
		"mode":             values["redis_mode"],
		"keyCount":         keyCount,
		"usedMemoryBytes":  parseInt64(values["used_memory"]),
		"usedMemoryHuman":  values["used_memory_human"],
		"maxMemoryBytes":   parseInt64(values["maxmemory"]),
		"connectedClients": parseInt64(values["connected_clients"]),
		"expiringKeys":     redisExpiringKeys(values),
		"hitRate":          hitRate,
		"uptimeSeconds":    parseInt64(values["uptime_in_seconds"]),
		"latencyMs":        time.Since(started).Milliseconds(),
	})
}

func (s *Server) redisKeys(w http.ResponseWriter, r *http.Request) {
	client := s.redis.AdminClient()
	pattern := strings.TrimSpace(r.URL.Query().Get("pattern"))
	if pattern == "" {
		pattern = "*"
	}
	if len(pattern) > redisMaxPattern {
		writeError(w, http.StatusBadRequest, "redis_pattern_too_long", "Redis scan pattern is too long")
		return
	}
	typeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	cursor, err := strconv.ParseUint(defaultString(r.URL.Query().Get("cursor"), "0"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", "cursor must be an unsigned integer")
		return
	}
	limit, ok := redisLimit(w, r.URL.Query().Get("limit"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	items := make([]redisKeySummary, 0, limit)
	next := cursor
	for rounds := 0; rounds < 32 && len(items) < limit; rounds++ {
		keys, nextCursor, scanErr := client.Scan(ctx, next, pattern, int64(limit*2)).Result()
		if scanErr != nil {
			writeError(w, http.StatusServiceUnavailable, "redis_unavailable", scanErr.Error())
			return
		}
		next = nextCursor
		for _, key := range keys {
			summary, summaryErr := redisSummary(ctx, client, key)
			if errors.Is(summaryErr, redis.Nil) {
				continue
			}
			if summaryErr != nil {
				writeError(w, http.StatusServiceUnavailable, "redis_read_failed", summaryErr.Error())
				return
			}
			if typeFilter != "" && typeFilter != "all" && summary.Type != typeFilter {
				continue
			}
			items = append(items, summary)
			if len(items) == limit {
				break
			}
		}
		if next == 0 {
			break
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	writeJSON(w, http.StatusOK, map[string]any{"keys": items, "cursor": strconv.FormatUint(cursor, 10), "nextCursor": strconv.FormatUint(next, 10)})
}

func (s *Server) redisKeyDetail(w http.ResponseWriter, r *http.Request) {
	key, ok := redisKeyParam(w, r.URL.Query().Get("key"))
	if !ok {
		return
	}
	limit, valid := redisLimit(w, r.URL.Query().Get("limit"))
	if !valid {
		return
	}
	cursor := defaultString(r.URL.Query().Get("cursor"), "0")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	detail, err := loadRedisDetail(ctx, s.redis.AdminClient(), key, cursor, limit)
	if errors.Is(err, redis.Nil) {
		writeError(w, http.StatusNotFound, "redis_key_not_found", "Redis key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "redis_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) redisMutateValue(w http.ResponseWriter, r *http.Request) {
	var input redisMutationInput
	if !decode(w, r, &input) {
		return
	}
	key, ok := redisKeyParam(w, input.Key)
	if !ok {
		return
	}
	if len(input.Value) > redisMaxValueSize || len(input.Field) > redisMaxValueSize || len(input.Member) > redisMaxValueSize {
		writeError(w, http.StatusRequestEntityTooLarge, "redis_value_too_large", "Redis field or value is too large")
		return
	}
	input.Key = key
	confirmationRequired, confirmationErr := redisMutationNeedsConfirmation(r.Context(), s.redis.AdminClient(), input)
	if confirmationErr != nil {
		writeRedisMutationError(w, confirmationErr)
		return
	}
	if confirmationRequired && input.ConfirmKey != key {
		writeError(w, http.StatusBadRequest, "confirmation_required", "confirmKey must match the Redis key")
		return
	}
	s.redisMu.Lock()
	defer s.redisMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client := s.redis.AdminClient()
	if !s.redisCheckVersion(w, ctx, key, input.Version) {
		return
	}
	var backup *redisBackup
	if s.isApplicationKey(key) {
		value, backupErr := captureRedisBackup(ctx, client, key)
		if backupErr != nil {
			writeRedisMutationError(w, backupErr)
			return
		}
		backup = &value
	}
	if mutationErr := applyRedisMutation(ctx, client, input); mutationErr != nil {
		writeRedisMutationError(w, mutationErr)
		return
	}
	prewarm, err := s.redisPrewarmAfterMutation(ctx, key, backup)
	if err != nil {
		writeError(w, http.StatusConflict, "redis_prewarm_failed", err.Error())
		return
	}
	detail, err := loadRedisDetail(ctx, client, key, "0", redisDefaultLimit)
	if err != nil {
		writeRedisMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": detail, "prewarm": prewarm})
}

func (s *Server) redisUpdateTTL(w http.ResponseWriter, r *http.Request) {
	var input redisTTLInput
	if !decode(w, r, &input) {
		return
	}
	key, ok := redisKeyParam(w, input.Key)
	if !ok {
		return
	}
	if input.ConfirmKey != key {
		writeError(w, http.StatusBadRequest, "confirmation_required", "confirmKey must match the Redis key")
		return
	}
	s.redisMu.Lock()
	defer s.redisMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client := s.redis.AdminClient()
	if !s.redisCheckVersion(w, ctx, key, input.Version) {
		return
	}
	var backup *redisBackup
	if s.isApplicationKey(key) {
		value, backupErr := captureRedisBackup(ctx, client, key)
		if backupErr != nil {
			writeRedisMutationError(w, backupErr)
			return
		}
		backup = &value
	}
	var err error
	if input.TTLSeconds == nil || *input.TTLSeconds <= 0 {
		err = client.Persist(ctx, key).Err()
	} else {
		if *input.TTLSeconds > redisMaxTTL {
			writeError(w, http.StatusBadRequest, "invalid_ttl", "ttlSeconds exceeds the 10 year limit")
			return
		}
		err = client.Expire(ctx, key, time.Duration(*input.TTLSeconds)*time.Second).Err()
	}
	if err != nil {
		writeRedisMutationError(w, err)
		return
	}
	prewarm, err := s.redisPrewarmAfterMutation(ctx, key, backup)
	if err != nil {
		writeError(w, http.StatusConflict, "redis_prewarm_failed", err.Error())
		return
	}
	detail, err := loadRedisDetail(ctx, client, key, "0", redisDefaultLimit)
	if err != nil {
		writeRedisMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": detail, "prewarm": prewarm})
}

func (s *Server) redisRename(w http.ResponseWriter, r *http.Request) {
	var input redisRenameInput
	if !decode(w, r, &input) {
		return
	}
	key, ok := redisKeyParam(w, input.Key)
	if !ok {
		return
	}
	newKey, ok := redisKeyParam(w, input.NewKey)
	if !ok {
		return
	}
	if input.ConfirmKey != key {
		writeError(w, http.StatusBadRequest, "confirmation_required", "confirmKey must match the Redis key")
		return
	}
	if s.isApplicationKey(key) != s.isApplicationKey(newKey) {
		writeError(w, http.StatusBadRequest, "namespace_boundary", "renaming across the AI Watch namespace boundary is not allowed")
		return
	}
	s.redisMu.Lock()
	defer s.redisMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client := s.redis.AdminClient()
	if !s.redisCheckVersion(w, ctx, key, input.Version) {
		return
	}
	exists, err := client.Exists(ctx, newKey).Result()
	if err != nil {
		writeRedisMutationError(w, err)
		return
	}
	if exists > 0 {
		writeError(w, http.StatusConflict, "redis_key_exists", "destination Redis key already exists")
		return
	}
	if err = client.Rename(ctx, key, newKey).Err(); err != nil {
		writeRedisMutationError(w, err)
		return
	}
	if s.isApplicationKey(key) {
		if _, err = s.redis.Prewarm(ctx); err != nil {
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer rollbackCancel()
			rollbackErr := client.Rename(rollbackCtx, newKey, key).Err()
			if rollbackErr == nil {
				_, rollbackErr = s.redis.Prewarm(rollbackCtx)
			}
			writeError(w, http.StatusConflict, "redis_prewarm_failed", prewarmRollbackMessage(err, rollbackErr))
			return
		}
	}
	detail, err := loadRedisDetail(ctx, client, newKey, "0", redisDefaultLimit)
	if err != nil {
		writeRedisMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": detail})
}

func (s *Server) redisDelete(w http.ResponseWriter, r *http.Request) {
	var input redisDeleteInput
	if !decode(w, r, &input) {
		return
	}
	key, ok := redisKeyParam(w, input.Key)
	if !ok {
		return
	}
	if input.ConfirmKey != key {
		writeError(w, http.StatusBadRequest, "confirmation_required", "confirmKey must match the Redis key")
		return
	}
	s.redisMu.Lock()
	defer s.redisMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	client := s.redis.AdminClient()
	if !s.redisCheckVersion(w, ctx, key, input.Version) {
		return
	}
	var backup *redisBackup
	if s.isApplicationKey(key) {
		value, backupErr := captureRedisBackup(ctx, client, key)
		if backupErr != nil {
			writeRedisMutationError(w, backupErr)
			return
		}
		backup = &value
	}
	if deleteErr := client.Del(ctx, key).Err(); deleteErr != nil {
		writeRedisMutationError(w, deleteErr)
		return
	}
	prewarm, err := s.redisPrewarmAfterMutation(ctx, key, backup)
	if err != nil {
		writeError(w, http.StatusConflict, "redis_prewarm_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "key": key, "prewarm": prewarm})
}

func (s *Server) redisPrewarm(w http.ResponseWriter, r *http.Request) {
	s.redisMu.Lock()
	defer s.redisMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	started := time.Now()
	result, err := s.redis.Prewarm(ctx)
	if err != nil {
		writeError(w, http.StatusConflict, "redis_prewarm_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"durationMs": time.Since(started).Milliseconds(), "snapshots": result})
}

func (s *Server) redisCheckVersion(w http.ResponseWriter, ctx context.Context, key, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		writeError(w, http.StatusBadRequest, "version_required", "version is required")
		return false
	}
	version, err := redisVersion(ctx, s.redis.AdminClient(), key)
	if errors.Is(err, redis.Nil) {
		writeError(w, http.StatusNotFound, "redis_key_not_found", "Redis key not found")
		return false
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "redis_read_failed", err.Error())
		return false
	}
	if version != expected {
		writeError(w, http.StatusConflict, "redis_version_conflict", "Redis key changed; refresh before retrying")
		return false
	}
	return true
}

func (s *Server) redisPrewarmAfterMutation(ctx context.Context, key string, backup *redisBackup) (*store.RedisPrewarmResult, error) {
	if !s.isApplicationKey(key) {
		return nil, nil
	}
	result, err := s.redis.Prewarm(ctx)
	if err == nil {
		return &result, nil
	}
	if backup == nil {
		return nil, fmt.Errorf("application prewarm failed without a rollback snapshot: %v", err)
	}
	rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer rollbackCancel()
	rollbackErr := restoreRedisBackup(rollbackCtx, s.redis.AdminClient(), *backup)
	if rollbackErr == nil {
		_, rollbackErr = s.redis.Prewarm(rollbackCtx)
	}
	return nil, errors.New(prewarmRollbackMessage(err, rollbackErr))
}

func (s *Server) isApplicationKey(key string) bool {
	return strings.HasPrefix(key, s.redis.Prefix()+":")
}

func redisSummary(ctx context.Context, client redis.UniversalClient, key string) (redisKeySummary, error) {
	typeName, err := client.Type(ctx, key).Result()
	if err != nil {
		return redisKeySummary{}, err
	}
	if typeName == "none" {
		return redisKeySummary{}, redis.Nil
	}
	ttl, err := client.PTTL(ctx, key).Result()
	if err != nil {
		return redisKeySummary{}, err
	}
	size, err := redisValueSize(ctx, client, key, typeName)
	if err != nil {
		return redisKeySummary{}, err
	}
	memory, _ := client.MemoryUsage(ctx, key).Result()
	ttlMillis := ttl.Milliseconds()
	if ttl < 0 {
		ttlMillis = int64(ttl)
	}
	return redisKeySummary{Key: key, Type: typeName, TTLMillis: ttlMillis, Persistent: ttl == -1, Size: size, MemoryBytes: memory}, nil
}

func loadRedisDetail(ctx context.Context, client redis.UniversalClient, key, cursor string, limit int) (redisKeyDetail, error) {
	summary, err := redisSummary(ctx, client, key)
	if err != nil {
		return redisKeyDetail{}, err
	}
	version, err := redisVersion(ctx, client, key)
	if err != nil {
		return redisKeyDetail{}, err
	}
	detail := redisKeyDetail{redisKeySummary: summary, Version: version, Cursor: cursor}
	detail.Encoding, _ = client.ObjectEncoding(ctx, key).Result()
	switch summary.Type {
	case "string":
		value, getErr := client.Get(ctx, key).Result()
		if getErr != nil {
			return redisKeyDetail{}, getErr
		}
		if len(value) > redisMaxValueSize {
			detail.Value = value[:redisMaxValueSize]
			detail.Truncated = true
		} else {
			detail.Value = value
		}
	case "hash":
		position, parseErr := strconv.ParseUint(defaultString(cursor, "0"), 10, 64)
		if parseErr != nil {
			return redisKeyDetail{}, errors.New("cursor must be an unsigned integer")
		}
		values, next, scanErr := client.HScan(ctx, key, position, "*", int64(limit)).Result()
		if scanErr != nil {
			return redisKeyDetail{}, scanErr
		}
		entries := make([]redisHashEntry, 0, len(values)/2)
		for index := 0; index+1 < len(values); index += 2 {
			entries = append(entries, redisHashEntry{Field: values[index], Value: values[index+1]})
		}
		detail.Value = entries
		detail.NextCursor = strconv.FormatUint(next, 10)
	case "list":
		position, parseErr := strconv.ParseInt(defaultString(cursor, "0"), 10, 64)
		if parseErr != nil || position < 0 || position > int64(^uint64(0)>>1)-int64(limit) {
			return redisKeyDetail{}, errors.New("cursor must be a non-negative integer")
		}
		values, rangeErr := client.LRange(ctx, key, position, position+int64(limit)-1).Result()
		if rangeErr != nil {
			return redisKeyDetail{}, rangeErr
		}
		detail.Value = values
		if position+int64(len(values)) < summary.Size {
			detail.NextCursor = strconv.FormatInt(position+int64(len(values)), 10)
		} else {
			detail.NextCursor = "0"
		}
	case "set":
		position, parseErr := strconv.ParseUint(defaultString(cursor, "0"), 10, 64)
		if parseErr != nil {
			return redisKeyDetail{}, errors.New("cursor must be an unsigned integer")
		}
		values, next, scanErr := client.SScan(ctx, key, position, "*", int64(limit)).Result()
		if scanErr != nil {
			return redisKeyDetail{}, scanErr
		}
		sort.Strings(values)
		detail.Value = values
		detail.NextCursor = strconv.FormatUint(next, 10)
	case "zset":
		position, parseErr := strconv.ParseInt(defaultString(cursor, "0"), 10, 64)
		if parseErr != nil || position < 0 || position > int64(^uint64(0)>>1)-int64(limit) {
			return redisKeyDetail{}, errors.New("cursor must be a non-negative integer")
		}
		values, rangeErr := client.ZRangeWithScores(ctx, key, position, position+int64(limit)-1).Result()
		if rangeErr != nil {
			return redisKeyDetail{}, rangeErr
		}
		entries := make([]redisZSetEntry, 0, len(values))
		for _, value := range values {
			entries = append(entries, redisZSetEntry{Member: fmt.Sprint(value.Member), Score: value.Score})
		}
		detail.Value = entries
		if position+int64(len(values)) < summary.Size {
			detail.NextCursor = strconv.FormatInt(position+int64(len(values)), 10)
		} else {
			detail.NextCursor = "0"
		}
	}
	return detail, nil
}

func applyRedisMutation(ctx context.Context, client redis.UniversalClient, input redisMutationInput) error {
	switch input.Operation {
	case "string:set":
		return client.Set(ctx, input.Key, input.Value, redis.KeepTTL).Err()
	case "hash:set":
		if input.Field == "" {
			return errors.New("field is required")
		}
		return client.HSet(ctx, input.Key, input.Field, input.Value).Err()
	case "hash:delete":
		if input.Field == "" {
			return errors.New("field is required")
		}
		return client.HDel(ctx, input.Key, input.Field).Err()
	case "list:set":
		if input.Index == nil {
			return errors.New("index is required")
		}
		return client.LSet(ctx, input.Key, *input.Index, input.Value).Err()
	case "list:append":
		return client.RPush(ctx, input.Key, input.Value).Err()
	case "list:prepend":
		return client.LPush(ctx, input.Key, input.Value).Err()
	case "list:delete":
		if input.Index == nil {
			return errors.New("index is required")
		}
		marker := "__ai_watch_delete__:" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := client.LSet(ctx, input.Key, *input.Index, marker).Err(); err != nil {
			return err
		}
		return client.LRem(ctx, input.Key, 1, marker).Err()
	case "set:add":
		return client.SAdd(ctx, input.Key, input.Member).Err()
	case "set:delete":
		return client.SRem(ctx, input.Key, input.Member).Err()
	case "zset:set":
		if input.Score == nil {
			return errors.New("score is required")
		}
		return client.ZAdd(ctx, input.Key, redis.Z{Member: input.Member, Score: *input.Score}).Err()
	case "zset:delete":
		return client.ZRem(ctx, input.Key, input.Member).Err()
	default:
		return errors.New("unsupported Redis mutation operation")
	}
}

func redisMutationNeedsConfirmation(ctx context.Context, client redis.UniversalClient, input redisMutationInput) (bool, error) {
	switch input.Operation {
	case "string:set", "hash:delete", "list:set", "list:delete", "set:delete", "zset:delete":
		return true, nil
	case "hash:set":
		return client.HExists(ctx, input.Key, input.Field).Result()
	case "zset:set":
		_, err := client.ZScore(ctx, input.Key, input.Member).Result()
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return err == nil, err
	default:
		return false, nil
	}
}

func captureRedisBackup(ctx context.Context, client redis.UniversalClient, key string) (redisBackup, error) {
	typeName, err := client.Type(ctx, key).Result()
	if err != nil {
		return redisBackup{}, err
	}
	if typeName == "none" {
		return redisBackup{}, redis.Nil
	}
	ttl, err := client.PTTL(ctx, key).Result()
	if err != nil {
		return redisBackup{}, err
	}
	backup := redisBackup{key: key, typeName: typeName, ttl: ttl}
	switch typeName {
	case "string":
		backup.stringV, err = client.Get(ctx, key).Result()
	case "hash":
		backup.hashV, err = client.HGetAll(ctx, key).Result()
	case "list":
		backup.listV, err = client.LRange(ctx, key, 0, -1).Result()
	case "set":
		backup.setV, err = client.SMembers(ctx, key).Result()
		sort.Strings(backup.setV)
	case "zset":
		backup.zsetV, err = client.ZRangeWithScores(ctx, key, 0, -1).Result()
	default:
		err = fmt.Errorf("Redis type %s is read-only", typeName)
	}
	return backup, err
}

func restoreRedisBackup(ctx context.Context, client redis.UniversalClient, backup redisBackup) error {
	if err := client.Del(ctx, backup.key).Err(); err != nil {
		return err
	}
	var err error
	switch backup.typeName {
	case "string":
		err = client.Set(ctx, backup.key, backup.stringV, 0).Err()
	case "hash":
		values := make([]any, 0, len(backup.hashV)*2)
		fields := make([]string, 0, len(backup.hashV))
		for field := range backup.hashV {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			values = append(values, field, backup.hashV[field])
		}
		if len(values) > 0 {
			err = client.HSet(ctx, backup.key, values...).Err()
		}
	case "list":
		if len(backup.listV) > 0 {
			values := make([]any, len(backup.listV))
			for index, value := range backup.listV {
				values[index] = value
			}
			err = client.RPush(ctx, backup.key, values...).Err()
		}
	case "set":
		if len(backup.setV) > 0 {
			values := make([]any, len(backup.setV))
			for index, value := range backup.setV {
				values[index] = value
			}
			err = client.SAdd(ctx, backup.key, values...).Err()
		}
	case "zset":
		if len(backup.zsetV) > 0 {
			err = client.ZAdd(ctx, backup.key, backup.zsetV...).Err()
		}
	default:
		err = fmt.Errorf("Redis type %s cannot be restored", backup.typeName)
	}
	if err != nil {
		return err
	}
	if backup.ttl > 0 {
		return client.PExpire(ctx, backup.key, backup.ttl).Err()
	}
	return nil
}

func redisVersion(ctx context.Context, client redis.UniversalClient, key string) (string, error) {
	typeName, err := client.Type(ctx, key).Result()
	if err != nil {
		return "", err
	}
	if typeName == "none" {
		return "", redis.Nil
	}
	payload := map[string]any{"type": typeName}
	switch typeName {
	case "string":
		size, sizeErr := client.StrLen(ctx, key).Result()
		if sizeErr != nil {
			return "", sizeErr
		}
		prefix, readErr := client.GetRange(ctx, key, 0, 4095).Result()
		if readErr != nil {
			return "", readErr
		}
		suffix := ""
		if size > 4096 {
			suffix, readErr = client.GetRange(ctx, key, maxInt64(0, size-4096), size-1).Result()
			if readErr != nil {
				return "", readErr
			}
		}
		payload["size"], payload["prefix"], payload["suffix"] = size, prefix, suffix
	case "hash":
		size, sizeErr := client.HLen(ctx, key).Result()
		values, _, scanErr := client.HScan(ctx, key, 0, "*", 32).Result()
		if sizeErr != nil || scanErr != nil {
			return "", errors.Join(sizeErr, scanErr)
		}
		pairs := make([]redisHashEntry, 0, len(values)/2)
		for index := 0; index+1 < len(values); index += 2 {
			pairs = append(pairs, redisHashEntry{Field: values[index], Value: values[index+1]})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Field < pairs[j].Field })
		payload["size"], payload["sample"] = size, pairs
	case "list":
		size, sizeErr := client.LLen(ctx, key).Result()
		first, firstErr := client.LRange(ctx, key, 0, 15).Result()
		last, lastErr := client.LRange(ctx, key, -16, -1).Result()
		if sizeErr != nil || firstErr != nil || lastErr != nil {
			return "", errors.Join(sizeErr, firstErr, lastErr)
		}
		payload["size"], payload["first"], payload["last"] = size, first, last
	case "set":
		size, sizeErr := client.SCard(ctx, key).Result()
		values, _, scanErr := client.SScan(ctx, key, 0, "*", 32).Result()
		if sizeErr != nil || scanErr != nil {
			return "", errors.Join(sizeErr, scanErr)
		}
		sort.Strings(values)
		payload["size"], payload["sample"] = size, values
	case "zset":
		size, sizeErr := client.ZCard(ctx, key).Result()
		first, firstErr := client.ZRangeWithScores(ctx, key, 0, 15).Result()
		last, lastErr := client.ZRangeWithScores(ctx, key, -16, -1).Result()
		if sizeErr != nil || firstErr != nil || lastErr != nil {
			return "", errors.Join(sizeErr, firstErr, lastErr)
		}
		payload["size"], payload["first"], payload["last"] = size, first, last
	default:
		payload["key"] = key
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:12]), nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func redisValueSize(ctx context.Context, client redis.UniversalClient, key, typeName string) (int64, error) {
	switch typeName {
	case "string":
		return client.StrLen(ctx, key).Result()
	case "hash":
		return client.HLen(ctx, key).Result()
	case "list":
		return client.LLen(ctx, key).Result()
	case "set":
		return client.SCard(ctx, key).Result()
	case "zset":
		return client.ZCard(ctx, key).Result()
	default:
		return 0, nil
	}
}

func redisKeyParam(w http.ResponseWriter, raw string) (string, bool) {
	key := raw
	if key == "" {
		writeError(w, http.StatusBadRequest, "redis_key_required", "Redis key is required")
		return "", false
	}
	if len(key) > redisMaxKeyLength {
		writeError(w, http.StatusBadRequest, "redis_key_too_long", "Redis key is too long")
		return "", false
	}
	return key, true
}

func redisLimit(w http.ResponseWriter, raw string) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return redisDefaultLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > redisMaxLimit {
		writeError(w, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be between 1 and %d", redisMaxLimit))
		return 0, false
	}
	return limit, true
}

func writeRedisMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, redis.Nil) {
		writeError(w, http.StatusNotFound, "redis_key_not_found", "Redis key not found")
		return
	}
	writeError(w, http.StatusBadRequest, "redis_mutation_failed", err.Error())
}

func prewarmRollbackMessage(prewarmErr, rollbackErr error) string {
	if rollbackErr == nil {
		return fmt.Sprintf("application prewarm failed and the Redis change was rolled back: %v", prewarmErr)
	}
	return fmt.Sprintf("application prewarm failed and rollback also failed: %v; rollback: %v", prewarmErr, rollbackErr)
}

func parseRedisInfo(info string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	return values
}

func redisExpiringKeys(values map[string]string) int64 {
	var total int64
	for key, value := range values {
		if !strings.HasPrefix(key, "db") {
			continue
		}
		for _, part := range strings.Split(value, ",") {
			name, raw, ok := strings.Cut(part, "=")
			if ok && name == "expires" {
				total += parseInt64(raw)
			}
		}
	}
	return total
}

func parseInt64(value string) int64 {
	number, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return number
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

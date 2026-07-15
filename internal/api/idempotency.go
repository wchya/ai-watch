package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ai-watch/internal/store"
)

const idempotencyTTL = 24 * time.Hour

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

type idempotentResponse struct {
	status  int
	headers map[string]string
	body    []byte
}

func (s *Server) idempotencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" || !idempotencyKeyPattern.MatchString(key) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Idempotency-Key", key)
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		hash := sha256.New()
		hash.Write([]byte(r.Method))
		hash.Write([]byte("\n"))
		hash.Write([]byte(r.URL.RequestURI()))
		hash.Write([]byte("\n"))
		hash.Write(body)
		fingerprint := hex.EncodeToString(hash.Sum(nil))
		record, owner, err := s.claimIdempotency(key, fingerprint)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if !owner {
			if record.Fingerprint != fingerprint {
				writeError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key 已用于不同请求")
				return
			}
			deadline := time.Now().Add(30 * time.Second)
			for record.Pending && time.Now().Before(deadline) {
				time.Sleep(25 * time.Millisecond)
				record, _ = s.readIdempotency(key)
			}
			if record.Pending {
				writeError(w, http.StatusConflict, "idempotency_in_progress", "相同操作仍在执行中")
				return
			}
			if record.Status > 0 {
				replayIdempotent(w, record)
				return
			}
		}
		capture := &idempotencyWriter{header: make(http.Header), body: bytes.NewBuffer(nil), status: http.StatusOK}
		defer func() {
			if recovered := recover(); recovered != nil {
				_ = s.completeIdempotency(key, store.IdempotencyRecord{Fingerprint: fingerprint, Status: http.StatusInternalServerError, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"error":{"code":"internal_error","message":"request failed"}}`)})
				panic(recovered)
			}
		}()
		next.ServeHTTP(capture, r)
		result := store.IdempotencyRecord{Fingerprint: fingerprint, Status: capture.status, Headers: map[string]string{"Content-Type": capture.header.Get("Content-Type")}, Body: capture.body.Bytes()}
		_ = s.completeIdempotency(key, result)
		for name, values := range capture.header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
	})
}

type idempotencyWriter struct {
	header http.Header
	body   *bytes.Buffer
	status int
	wrote  bool
}

func (w *idempotencyWriter) Header() http.Header { return w.header }
func (w *idempotencyWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status = status
		w.wrote = true
	}
}
func (w *idempotencyWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(body)
}

func replayIdempotent(w http.ResponseWriter, record store.IdempotencyRecord) {
	for key, value := range record.Headers {
		w.Header().Set(key, value)
	}
	w.WriteHeader(record.Status)
	_, _ = w.Write(record.Body)
}
func (s *Server) claimIdempotency(key, fingerprint string) (store.IdempotencyRecord, bool, error) {
	if s.redis != nil {
		return s.redis.ClaimIdempotency(key, fingerprint, idempotencyTTL)
	}
	s.idempotencyMu.Lock()
	defer s.idempotencyMu.Unlock()
	if record, ok := s.idempotency[key]; ok {
		return record, false, nil
	}
	record := store.IdempotencyRecord{Fingerprint: fingerprint, Pending: true}
	s.idempotency[key] = record
	return record, true, nil
}
func (s *Server) readIdempotency(key string) (store.IdempotencyRecord, error) {
	if s.redis != nil {
		return s.redis.ReadIdempotency(key)
	}
	s.idempotencyMu.Lock()
	defer s.idempotencyMu.Unlock()
	record, ok := s.idempotency[key]
	if !ok {
		return store.IdempotencyRecord{}, io.EOF
	}
	return record, nil
}
func (s *Server) completeIdempotency(key string, record store.IdempotencyRecord) error {
	if s.redis != nil {
		return s.redis.CompleteIdempotency(key, record, idempotencyTTL)
	}
	s.idempotencyMu.Lock()
	s.idempotency[key] = record
	s.idempotencyMu.Unlock()
	return nil
}

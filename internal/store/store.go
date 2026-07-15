package store

import (
	"time"

	"ai-watch/internal/domain"
)

// Store is the persistence boundary used by jobs and API packages. JSON is
// retained as the SQLite migration source; Redis is the production backend.
type Store interface {
	Close() error
	Err() error
	LoadSettings() (domain.Settings, error)
	SaveSettings(domain.Settings) error
	ListProviderExamples() ([]domain.ProviderExample, error)
	UpsertProviderExample(domain.ProviderExample) (domain.ProviderExample, error)
	DeleteProviderExample(string) (bool, error)
	ListSchedules() ([]domain.Schedule, error)
	GetSchedule(string) (domain.Schedule, error)
	UpsertSchedule(domain.Schedule) (domain.Schedule, error)
	DeleteSchedule(string) (bool, error)
	MarkScheduleRun(string, string, string, string, time.Time) error
	LoadSummaries() ([]domain.Summary, error)
	SaveSummary(domain.Summary, int) error
	SaveEvent(Event, ...EventRetention) error
	ListEvents(EventFilter) ([]Event, error)
	CountEvents(EventFilter) (int64, error)
	ClearEvents() (int64, error)
	RetainEvents(EventRetention) (RetentionResult, error)
	Diagnostics() (Diagnostics, error)
}

// JobEventStore is the optional cache boundary for replayable per-job runtime
// events. Redis implements it in production; other stores may omit it.
type JobEventStore interface {
	SaveJobEvent(string, domain.Event, JobEventRetention) error
	ListJobEvents(string, uint64) ([]domain.Event, error)
}

type JobEventRetention struct {
	TTL      time.Duration
	MaxRows  int
	MaxBytes int64
}

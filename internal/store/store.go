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

type TestScenarioStore interface {
	ListTestScenarios() ([]domain.TestScenario, error)
	GetTestScenario(string) (domain.TestScenario, error)
	UpsertTestScenario(domain.TestScenario) (domain.TestScenario, error)
	DeleteTestScenario(string) (bool, error)
}

type ProviderGroupStore interface {
	ListProviderGroups() ([]domain.ProviderGroup, error)
	GetProviderGroup(string) (domain.ProviderGroup, error)
	UpsertProviderGroup(domain.ProviderGroup) (domain.ProviderGroup, error)
	DeleteProviderGroup(string) (bool, error)
}

type IncidentStore interface {
	ListIncidents(string) ([]domain.Incident, error)
	GetIncident(string) (domain.Incident, error)
	FindOpenIncident(string, string) (domain.Incident, error)
	UpsertIncident(domain.Incident) (domain.Incident, error)
}

type IncidentPostmortemStore interface {
	GetIncidentPostmortem(string) (domain.IncidentPostmortem, error)
	UpsertIncidentPostmortem(domain.IncidentPostmortem) (domain.IncidentPostmortem, error)
}

type NotificationRoutingStore interface {
	ListNotificationChannels() ([]domain.NotificationChannel, error)
	GetNotificationChannel(string) (domain.NotificationChannel, error)
	UpsertNotificationChannel(domain.NotificationChannel) (domain.NotificationChannel, error)
	DeleteNotificationChannel(string) (bool, error)
	LoadNotificationRoutes() (domain.NotificationRoutes, error)
	SaveNotificationRoutes(domain.NotificationRoutes) (domain.NotificationRoutes, error)
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

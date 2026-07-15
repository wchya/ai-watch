package reliability

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/store"
)

var ErrInvalidRange = errors.New("invalid reliability range")

type EventReader interface {
	ListEvents(store.EventFilter) ([]store.Event, error)
}

type Coverage struct {
	RequestedStart time.Time `json:"requestedStart"`
	End            time.Time `json:"end"`
	RetentionDays  int       `json:"retentionDays"`
	Partial        bool      `json:"partial"`
	SampleCount    int       `json:"sampleCount"`
}

type Counts struct {
	Success     int `json:"success"`
	Timeout     int `json:"timeout"`
	Overloaded  int `json:"overloaded"`
	Unmatched   int `json:"unmatched"`
	Fatal       int `json:"fatal"`
	StartFailed int `json:"startFailed"`
	Stopped     int `json:"stopped"`
}

func (c Counts) completed() int {
	return c.Success + c.Timeout + c.Overloaded + c.Unmatched + c.Fatal + c.StartFailed
}
func (c Counts) failures() int { return c.completed() - c.Success }

type Metrics struct {
	Requests               int      `json:"requests"`
	Completed              int      `json:"completed"`
	SuccessRate            *float64 `json:"successRate,omitempty"`
	AverageDurationMillis  *float64 `json:"averageDurationMillis,omitempty"`
	P95DurationMillis      *int64   `json:"p95DurationMillis,omitempty"`
	MaxConsecutiveFailures int      `json:"maxConsecutiveFailures"`
	ConsecutiveFailures    int      `json:"consecutiveFailures"`
	Counts                 Counts   `json:"counts"`
}

type Provider struct {
	Key            string         `json:"key"`
	CLI            string         `json:"cli"`
	ProviderID     string         `json:"providerId"`
	Name           string         `json:"name"`
	Model          string         `json:"model,omitempty"`
	Historical     bool           `json:"historical"`
	LastStatus     string         `json:"lastStatus,omitempty"`
	LastRequestAt  *time.Time     `json:"lastRequestAt,omitempty"`
	LastSuccessAt  *time.Time     `json:"lastSuccessAt,omitempty"`
	LastFailureAt  *time.Time     `json:"lastFailureAt,omitempty"`
	Metrics        Metrics        `json:"metrics"`
	Recommendation Recommendation `json:"recommendation"`
}

type Recommendation struct {
	Level   string   `json:"level"`
	Title   string   `json:"title"`
	Reasons []string `json:"reasons"`
	Action  string   `json:"action"`
}

type Bucket struct {
	Start                 time.Time `json:"start"`
	Requests              int       `json:"requests"`
	Successes             int       `json:"successes"`
	Failures              int       `json:"failures"`
	Stopped               int       `json:"stopped"`
	SuccessRate           *float64  `json:"successRate,omitempty"`
	AverageDurationMillis *float64  `json:"averageDurationMillis,omitempty"`
}

type Response struct {
	Range       string     `json:"range"`
	GeneratedAt time.Time  `json:"generatedAt"`
	Coverage    Coverage   `json:"coverage"`
	Overall     Metrics    `json:"overall"`
	Providers   []Provider `json:"providers"`
	Buckets     []Bucket   `json:"buckets"`
	Anomalies   []Bucket   `json:"anomalies"`
}

type sample struct {
	at          time.Time
	providerKey string
	cli         string
	providerID  string
	name        string
	model       string
	status      string
	duration    int64
	hasDuration bool
}

type accumulator struct {
	counts    Counts
	durations []int64
	samples   []sample
}

func Build(reader EventReader, selectedRange string, now time.Time, activeProviders map[string]bool, retentionDays int) (Response, error) {
	duration, bucketDuration, bucketCount, err := rangeSpec(selectedRange)
	if err != nil {
		return Response{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	start := now.Add(-duration)
	events, err := loadEvents(reader, start, now)
	if err != nil {
		return Response{}, err
	}
	samples := make([]sample, 0, len(events))
	for _, event := range events {
		if value, ok := eventSample(event); ok {
			samples = append(samples, value)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].at.Before(samples[j].at) })

	buckets := make([]Bucket, bucketCount)
	bucketDurations := make([][]int64, bucketCount)
	for index := range buckets {
		buckets[index].Start = start.Add(time.Duration(index) * bucketDuration)
	}
	overall := accumulator{}
	byProvider := map[string]*accumulator{}
	providerMeta := map[string]sample{}
	for _, value := range samples {
		addSample(&overall, value)
		if byProvider[value.providerKey] == nil {
			byProvider[value.providerKey] = &accumulator{}
		}
		addSample(byProvider[value.providerKey], value)
		providerMeta[value.providerKey] = value
		index := int(value.at.Sub(start) / bucketDuration)
		if index < 0 {
			continue
		}
		if index >= len(buckets) {
			index = len(buckets) - 1
		}
		addBucket(&buckets[index], &bucketDurations[index], value)
	}
	for index := range buckets {
		finishBucket(&buckets[index], bucketDurations[index])
	}

	providers := make([]Provider, 0, len(byProvider))
	for key, values := range byProvider {
		meta := providerMeta[key]
		provider := Provider{Key: key, CLI: meta.cli, ProviderID: meta.providerID, Name: meta.name, Model: meta.model, Historical: !activeProviders[key], Metrics: finishMetrics(*values)}
		if provider.Name == "" {
			provider.Name = providerLabel(meta.cli, meta.providerID)
		}
		for i := len(values.samples) - 1; i >= 0; i-- {
			item := values.samples[i]
			if provider.LastRequestAt == nil {
				at := item.at
				provider.LastRequestAt = &at
				provider.LastStatus = item.status
			}
			if item.status == "success" && provider.LastSuccessAt == nil {
				at := item.at
				provider.LastSuccessAt = &at
			}
			if isFailure(item.status) && provider.LastFailureAt == nil {
				at := item.at
				provider.LastFailureAt = &at
			}
		}
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		left, right := providers[i].Metrics, providers[j].Metrics
		if (left.Completed >= 5) != (right.Completed >= 5) {
			return left.Completed >= 5
		}
		if left.SuccessRate == nil {
			return false
		}
		if right.SuccessRate == nil {
			return true
		}
		if *left.SuccessRate != *right.SuccessRate {
			return *left.SuccessRate > *right.SuccessRate
		}
		return left.Completed > right.Completed
	})
	applyRecommendations(providers)
	anomalies := append([]Bucket(nil), buckets...)
	sort.Slice(anomalies, func(i, j int) bool {
		if anomalies[i].Failures != anomalies[j].Failures {
			return anomalies[i].Failures > anomalies[j].Failures
		}
		return anomalies[i].Requests > anomalies[j].Requests
	})
	filtered := anomalies[:0]
	for _, bucket := range anomalies {
		if bucket.Failures > 0 {
			filtered = append(filtered, bucket)
		}
	}
	if len(filtered) > 5 {
		filtered = filtered[:5]
	}

	return Response{
		Range: selectedRange, GeneratedAt: now,
		Coverage: Coverage{RequestedStart: start, End: now, RetentionDays: retentionDays, Partial: retentionDays > 0 && duration > time.Duration(retentionDays)*24*time.Hour, SampleCount: len(samples)},
		Overall:  finishMetrics(overall), Providers: providers, Buckets: buckets, Anomalies: filtered,
	}, nil
}

func applyRecommendations(providers []Provider) {
	var bestP95 *int64
	recommended := -1
	for index := range providers {
		metrics := providers[index].Metrics
		if metrics.Completed < 5 || metrics.SuccessRate == nil {
			continue
		}
		if *metrics.SuccessRate >= .9 && metrics.P95DurationMillis != nil && (bestP95 == nil || *metrics.P95DurationMillis < *bestP95) {
			value := *metrics.P95DurationMillis
			bestP95 = &value
		}
		if recommended < 0 && *metrics.SuccessRate >= .95 && metrics.ConsecutiveFailures == 0 {
			recommended = index
		}
	}
	for index := range providers {
		provider := &providers[index]
		metrics := provider.Metrics
		if metrics.Completed < 5 || metrics.SuccessRate == nil {
			provider.Recommendation = Recommendation{Level: "insufficient", Title: "样本不足", Reasons: []string{fmt.Sprintf("当前只有 %d 个完成样本，至少需要 5 个", metrics.Completed)}, Action: "继续运行测活或保活，积累更多样本后再判断"}
			continue
		}
		failureShare := float64(metrics.Counts.Timeout+metrics.Counts.Overloaded) / float64(metrics.Completed)
		if *metrics.SuccessRate < .7 || metrics.ConsecutiveFailures >= 3 {
			reasons := []string{}
			if *metrics.SuccessRate < .7 {
				reasons = append(reasons, fmt.Sprintf("成功率仅 %.0f%%，低于 70%%", *metrics.SuccessRate*100))
			}
			if metrics.ConsecutiveFailures >= 3 {
				reasons = append(reasons, fmt.Sprintf("当前已连续失败 %d 次", metrics.ConsecutiveFailures))
			}
			action := "暂停作为主线路，检查凭证、配额、代理和上游状态"
			if provider.Historical {
				action = "仅保留历史参考，不要作为当前运行目标"
			}
			provider.Recommendation = Recommendation{Level: "pause", Title: "建议暂停", Reasons: reasons, Action: action}
			continue
		}
		reasons := []string{}
		if *metrics.SuccessRate < .9 {
			reasons = append(reasons, fmt.Sprintf("成功率 %.0f%%，低于 90%%", *metrics.SuccessRate*100))
		}
		if metrics.ConsecutiveFailures > 0 {
			reasons = append(reasons, fmt.Sprintf("当前连续失败 %d 次", metrics.ConsecutiveFailures))
		}
		if failureShare >= .2 {
			reasons = append(reasons, fmt.Sprintf("超时与过载占完成请求的 %.0f%%", failureShare*100))
		}
		if bestP95 != nil && metrics.P95DurationMillis != nil && *metrics.P95DurationMillis > *bestP95*2 {
			reasons = append(reasons, "P95 延迟超过最佳健康线路的两倍")
		}
		if len(reasons) > 0 {
			provider.Recommendation = Recommendation{Level: "observe", Title: "建议观察", Reasons: reasons, Action: "保留为备用线路，并持续观察下一时间窗"}
			continue
		}
		if index == recommended && !provider.Historical {
			provider.Recommendation = Recommendation{Level: "recommended", Title: "推荐主线路", Reasons: []string{fmt.Sprintf("成功率 %.0f%%，当前无连续失败", *metrics.SuccessRate*100)}, Action: "可优先用于测活、保活和计划任务"}
		} else {
			provider.Recommendation = Recommendation{Level: "healthy", Title: "状态健康", Reasons: []string{fmt.Sprintf("成功率 %.0f%%，未触发风险阈值", *metrics.SuccessRate*100)}, Action: "保持当前使用策略"}
		}
	}
}

func rangeSpec(value string) (time.Duration, time.Duration, int, error) {
	switch value {
	case "", "24h":
		return 24 * time.Hour, time.Hour, 24, nil
	case "7d":
		return 7 * 24 * time.Hour, 6 * time.Hour, 28, nil
	case "30d":
		return 30 * 24 * time.Hour, 24 * time.Hour, 30, nil
	default:
		return 0, 0, 0, ErrInvalidRange
	}
}

func loadEvents(reader EventReader, since, until time.Time) ([]store.Event, error) {
	const pageSize = 1000
	var result []store.Event
	for offset := 0; ; offset += pageSize {
		values, err := reader.ListEvents(store.EventFilter{Type: "request_end", Since: since, Until: until, Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		result = append(result, values...)
		if len(values) < pageSize {
			return result, nil
		}
	}
}

func eventSample(event store.Event) (sample, bool) {
	data := event.Data
	job, _ := data["job"].(map[string]any)
	cli := stringValue(job["cli"])
	if cli == "" {
		return sample{}, false
	}
	providerID := event.ProviderID
	keyID := providerID
	if keyID == "" {
		keyID = "current"
	}
	status := stringValue(data["classification"])
	if status == "" {
		status = stringValue(data["status"])
	}
	status = normalizeStatus(status)
	if status == "" {
		return sample{}, false
	}
	duration, hasDuration := int64Value(data["durationMillis"])
	return sample{at: event.At.UTC(), providerKey: cli + ":" + keyID, cli: cli, providerID: providerID, name: stringValue(job["providerName"]), model: stringValue(job["model"]), status: status, duration: duration, hasDuration: hasDuration && duration >= 0}, true
}

func normalizeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success":
		return "success"
	case "timeout":
		return "timeout"
	case "overloaded":
		return "overloaded"
	case "unmatched", "failed":
		return "unmatched"
	case "fatal":
		return "fatal"
	case "start_failed":
		return "start_failed"
	case "stopped":
		return "stopped"
	default:
		return ""
	}
}

func addSample(acc *accumulator, value sample) {
	acc.samples = append(acc.samples, value)
	addCount(&acc.counts, value.status)
	if value.hasDuration && value.status != "stopped" {
		acc.durations = append(acc.durations, value.duration)
	}
}

func addCount(counts *Counts, status string) {
	switch status {
	case "success":
		counts.Success++
	case "timeout":
		counts.Timeout++
	case "overloaded":
		counts.Overloaded++
	case "unmatched":
		counts.Unmatched++
	case "fatal":
		counts.Fatal++
	case "start_failed":
		counts.StartFailed++
	case "stopped":
		counts.Stopped++
	}
}

func finishMetrics(acc accumulator) Metrics {
	metrics := Metrics{Requests: len(acc.samples), Completed: acc.counts.completed(), Counts: acc.counts}
	if metrics.Completed > 0 {
		rate := float64(acc.counts.Success) / float64(metrics.Completed)
		metrics.SuccessRate = &rate
	}
	if len(acc.durations) > 0 {
		var total int64
		for _, value := range acc.durations {
			total += value
		}
		average := float64(total) / float64(len(acc.durations))
		metrics.AverageDurationMillis = &average
	}
	if len(acc.durations) >= 5 {
		values := append([]int64(nil), acc.durations...)
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		p95 := values[int(math.Ceil(float64(len(values))*0.95))-1]
		metrics.P95DurationMillis = &p95
	}
	current := 0
	for _, value := range acc.samples {
		if value.status == "success" {
			current = 0
		} else if isFailure(value.status) {
			current++
			metrics.MaxConsecutiveFailures = max(metrics.MaxConsecutiveFailures, current)
		}
	}
	metrics.ConsecutiveFailures = current
	return metrics
}

func addBucket(bucket *Bucket, durations *[]int64, value sample) {
	bucket.Requests++
	if value.status == "success" {
		bucket.Successes++
	} else if value.status == "stopped" {
		bucket.Stopped++
	} else if isFailure(value.status) {
		bucket.Failures++
	}
	if value.hasDuration && value.status != "stopped" {
		*durations = append(*durations, value.duration)
	}
}

func finishBucket(bucket *Bucket, durations []int64) {
	completed := bucket.Successes + bucket.Failures
	if completed > 0 {
		rate := float64(bucket.Successes) / float64(completed)
		bucket.SuccessRate = &rate
	}
	if len(durations) > 0 {
		var total int64
		for _, value := range durations {
			total += value
		}
		average := float64(total) / float64(len(durations))
		bucket.AverageDurationMillis = &average
	}
}

func isFailure(status string) bool { return status != "" && status != "success" && status != "stopped" }
func providerLabel(cli, id string) string {
	if id == "" {
		if cli == "codex" {
			return "当前 Codex 配置"
		}
		if cli == "claude" {
			return "当前 Claude 配置"
		}
		return "当前配置"
	}
	return id
}
func stringValue(value any) string { text, _ := value.(string); return strings.TrimSpace(text) }
func int64Value(value any) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case int:
		return int64(number), true
	case float64:
		return int64(number), true
	case float32:
		return int64(number), true
	default:
		return 0, false
	}
}

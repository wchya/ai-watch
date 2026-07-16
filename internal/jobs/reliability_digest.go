package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ai-watch/internal/reliability"
	"ai-watch/internal/store"
)

type ReliabilityDigestPreview struct {
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Range       string    `json:"range"`
	GeneratedAt time.Time `json:"generatedAt"`
}

func (m *Manager) reliabilityDigestLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	_ = m.runReliabilityDigest(time.Now().UTC())
	for {
		select {
		case <-m.ctx.Done():
			return
		case now := <-ticker.C:
			_ = m.runReliabilityDigest(now.UTC())
		}
	}
}

func (m *Manager) ReliabilityDigestPreview() (ReliabilityDigestPreview, error) {
	m.mu.RLock()
	settings := m.settings
	m.mu.RUnlock()
	return m.buildReliabilityDigest(settings.ReliabilityDigestRange, time.Now().UTC(), settings.EventRetentionDays)
}

func (m *Manager) SendReliabilityDigest(ctx context.Context) (ReliabilityDigestPreview, error) {
	preview, err := m.ReliabilityDigestPreview()
	if err != nil {
		return preview, err
	}
	if err := m.sendRoutedMessage(ctx, "reliability_digest", preview.Title, preview.Content); err != nil {
		return preview, err
	}
	m.recordOperationalEvent(store.Event{At: time.Now().UTC(), Type: "reliability_digest_sent", Level: "success", Message: "可靠性摘要已手动发送", Data: map[string]any{"source": "manual", "range": preview.Range}})
	return preview, nil
}

func (m *Manager) runReliabilityDigest(now time.Time) error {
	m.mu.RLock()
	settings := m.settings
	m.mu.RUnlock()
	if !settings.ReliabilityDigestEnabled {
		return nil
	}
	location, err := time.LoadLocation(settings.ReliabilityDigestTimezone)
	if err != nil {
		return err
	}
	local := now.In(location)
	scheduled := time.Date(local.Year(), local.Month(), local.Day(), settings.ReliabilityDigestHour, settings.ReliabilityDigestMinute, 0, 0, location)
	if local.Before(scheduled) {
		return nil
	}
	date := local.Format("2006-01-02")
	m.digestMu.Lock()
	if m.digestSentDate == date {
		m.digestMu.Unlock()
		return nil
	}
	m.digestMu.Unlock()
	if m.digestAlreadySent(date) {
		m.digestMu.Lock()
		m.digestSentDate = date
		m.digestMu.Unlock()
		return nil
	}
	preview, err := m.buildReliabilityDigest(settings.ReliabilityDigestRange, now, settings.EventRetentionDays)
	if err == nil {
		err = m.sendRoutedMessage(m.ctx, "reliability_digest", preview.Title, preview.Content)
	}
	if err != nil {
		m.recordOperationalEvent(store.Event{At: now, Type: "reliability_digest_failed", Level: "error", Message: "定时可靠性摘要发送失败", Data: map[string]any{"source": "scheduled", "date": date, "range": settings.ReliabilityDigestRange, "error": err.Error()}})
		return err
	}
	m.digestMu.Lock()
	m.digestSentDate = date
	m.digestMu.Unlock()
	m.recordOperationalEvent(store.Event{At: now, Type: "reliability_digest_sent", Level: "success", Message: "定时可靠性摘要已发送", Data: map[string]any{"source": "scheduled", "date": date, "range": settings.ReliabilityDigestRange}})
	return nil
}

func (m *Manager) digestAlreadySent(date string) bool {
	events, err := m.store.ListEvents(store.EventFilter{Type: "reliability_digest_sent", Limit: 100})
	if err != nil {
		return false
	}
	for _, event := range events {
		if fmt.Sprint(event.Data["source"]) == "scheduled" && fmt.Sprint(event.Data["date"]) == date {
			return true
		}
	}
	return false
}

func (m *Manager) buildReliabilityDigest(selectedRange string, now time.Time, retentionDays int) (ReliabilityDigestPreview, error) {
	if err := m.FlushEvents(); err != nil {
		return ReliabilityDigestPreview{}, err
	}
	initial, err := reliability.Build(m.store, selectedRange, now, nil, retentionDays)
	if err != nil {
		return ReliabilityDigestPreview{}, err
	}
	active := map[string]bool{}
	for _, provider := range initial.Providers {
		active[provider.Key] = true
	}
	result, err := reliability.Build(m.store, selectedRange, now, active, retentionDays)
	if err != nil {
		return ReliabilityDigestPreview{}, err
	}
	rate := "样本不足"
	if result.Overall.SuccessRate != nil {
		rate = fmt.Sprintf("%.1f%%", *result.Overall.SuccessRate*100)
	}
	p95 := "样本不足"
	if result.Overall.P95DurationMillis != nil {
		p95 = fmt.Sprintf("%dms", *result.Overall.P95DurationMillis)
	}
	lines := []string{"### AI Watch 可靠性摘要", "", fmt.Sprintf("- 统计范围：%s", result.Range), fmt.Sprintf("- 完成样本：%d", result.Overall.Completed), fmt.Sprintf("- 整体成功率：%s", rate), fmt.Sprintf("- P95 延迟：%s", p95)}
	if result.Coverage.Partial {
		lines = append(lines, "- ⚠️ 当前时间窗仅有部分保留数据")
	}
	lines = append(lines, "", "#### Provider 建议")
	if len(result.Providers) == 0 {
		lines = append(lines, "- 暂无可靠性样本")
	}
	for index, provider := range result.Providers {
		if index >= 10 {
			lines = append(lines, fmt.Sprintf("- 另有 %d 条线路未展开", len(result.Providers)-index))
			break
		}
		lines = append(lines, fmt.Sprintf("- **%s**：%s — %s", provider.Name, provider.Recommendation.Title, provider.Recommendation.Action))
	}
	return ReliabilityDigestPreview{Title: "AI Watch 可靠性摘要", Content: strings.Join(lines, "\n"), Range: result.Range, GeneratedAt: result.GeneratedAt}, nil
}

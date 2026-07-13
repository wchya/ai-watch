package jobs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ai-watch/internal/domain"
)

const maxNotificationTargets = 200

var ErrNotificationsNotConfigured = errors.New("DingTalk webhook is not configured")
var ErrNotificationMessagesUnsupported = errors.New("configured notifier does not support status messages")

type messageNotifier interface {
	Send(context.Context, string, string) error
}

type notificationTarget struct {
	Job   domain.Job
	Count int
}

type notificationState struct {
	keepaliveStartedAt time.Time
	keepaliveSuccesses int
	keepaliveTargets   map[string]notificationTarget
	keepaliveDropped   int

	probeStartedAt time.Time
	probeAttempts  int
	probeTargets   map[string]notificationTarget
	probeDropped   int

	recoveries      map[string]notificationTarget
	recoveryDropped int
	recoveryTimer   *time.Timer
}

func (m *Manager) TestNotification(ctx context.Context) error {
	notifier, err := m.messageNotifier()
	if err != nil {
		return err
	}
	return notifier.Send(ctx, "AI Watch 通知测试", "### AI Watch 通知测试成功\n\n钉钉 Webhook 已通过 HTTP 状态和 errcode 校验。")
}

func (m *Manager) SendStatusSummary(ctx context.Context) error {
	notifier, err := m.messageNotifier()
	if err != nil {
		return err
	}
	return notifier.Send(ctx, "AI Watch 状态汇总", m.statusSummaryMarkdown())
}

func (m *Manager) messageNotifier() (messageNotifier, error) {
	m.mu.RLock()
	n := m.notifier
	m.mu.RUnlock()
	if n == nil || !n.Configured() {
		return nil, ErrNotificationsNotConfigured
	}
	value, ok := n.(messageNotifier)
	if !ok {
		return nil, ErrNotificationMessagesUnsupported
	}
	return value, nil
}

func (m *Manager) recordKeepaliveSuccess(job domain.Job) {
	now := time.Now().UTC()
	var title, content string
	m.mu.Lock()
	settings := m.settings
	if settings.KeepaliveSummarySeconds <= 0 && settings.KeepaliveSummarySuccesses <= 0 {
		m.notifications.keepaliveStartedAt = time.Time{}
		m.notifications.keepaliveSuccesses = 0
		m.notifications.keepaliveTargets = nil
		m.notifications.keepaliveDropped = 0
		m.mu.Unlock()
		return
	}
	if m.notifications.keepaliveStartedAt.IsZero() {
		m.notifications.keepaliveStartedAt = now
	}
	m.notifications.keepaliveSuccesses++
	addNotificationTarget(&m.notifications.keepaliveTargets, &m.notifications.keepaliveDropped, job)
	elapsed := now.Sub(m.notifications.keepaliveStartedAt)
	dueByTime := settings.KeepaliveSummarySeconds > 0 && elapsed >= time.Duration(settings.KeepaliveSummarySeconds)*time.Second
	dueByCount := settings.KeepaliveSummarySuccesses > 0 && m.notifications.keepaliveSuccesses >= settings.KeepaliveSummarySuccesses
	if dueByTime || dueByCount {
		title = "AI Watch 保活汇总"
		content = keepaliveSummaryMarkdown(now, elapsed, m.notifications.keepaliveSuccesses, m.notifications.keepaliveTargets, m.notifications.keepaliveDropped)
		m.notifications.keepaliveStartedAt = time.Time{}
		m.notifications.keepaliveSuccesses = 0
		m.notifications.keepaliveTargets = nil
		m.notifications.keepaliveDropped = 0
	}
	m.mu.Unlock()
	if content != "" {
		m.sendMessageAsync(title, content)
	}
}

func (m *Manager) recordProbeProgress(job domain.Job) {
	now := time.Now().UTC()
	var title, content string
	m.mu.Lock()
	seconds := m.settings.ProbeProgressSeconds
	if seconds <= 0 {
		m.clearProbeProgressLocked(job)
		m.mu.Unlock()
		return
	}
	if m.notifications.probeStartedAt.IsZero() {
		m.notifications.probeStartedAt = now
	}
	m.notifications.probeAttempts++
	addNotificationTarget(&m.notifications.probeTargets, &m.notifications.probeDropped, job)
	elapsed := now.Sub(m.notifications.probeStartedAt)
	if elapsed >= time.Duration(seconds)*time.Second {
		title = "AI Watch 测活进度"
		content = probeProgressMarkdown(now, elapsed, m.notifications.probeAttempts, m.notifications.probeTargets, m.notifications.probeDropped)
		m.notifications.probeStartedAt = time.Time{}
		m.notifications.probeAttempts = 0
		m.notifications.probeTargets = nil
		m.notifications.probeDropped = 0
	}
	m.mu.Unlock()
	if content != "" {
		m.sendMessageAsync(title, content)
	}
}

func (m *Manager) clearProbeProgress(job domain.Job) {
	m.mu.Lock()
	m.clearProbeProgressLocked(job)
	m.mu.Unlock()
}

func (m *Manager) clearProbeProgressLocked(job domain.Job) {
	key := notificationTargetKey(job)
	if target, ok := m.notifications.probeTargets[key]; ok {
		m.notifications.probeAttempts -= target.Count
		if m.notifications.probeAttempts < 0 {
			m.notifications.probeAttempts = 0
		}
	}
	delete(m.notifications.probeTargets, key)
	if len(m.notifications.probeTargets) == 0 {
		m.notifications.probeStartedAt = time.Time{}
		m.notifications.probeAttempts = 0
		m.notifications.probeTargets = nil
		m.notifications.probeDropped = 0
	}
}

func (m *Manager) queueRecovery(job domain.Job) {
	m.mu.RLock()
	mergeSeconds := m.settings.RecoveryMergeSeconds
	m.mu.RUnlock()
	if mergeSeconds <= 0 {
		m.clearProbeProgress(job)
		m.notify(job, domain.AttemptSuccess)
		return
	}
	if _, err := m.messageNotifier(); err != nil {
		// Preserve compatibility with simple notifiers while DingTalk-capable
		// notifiers use the merged recovery path below.
		m.notify(job, domain.AttemptSuccess)
		return
	}
	m.mu.Lock()
	addNotificationTarget(&m.notifications.recoveries, &m.notifications.recoveryDropped, job)
	m.clearProbeProgressLocked(job)
	if m.notifications.recoveryTimer == nil {
		delay := time.Duration(m.settings.RecoveryMergeSeconds) * time.Second
		if delay < time.Second {
			delay = time.Second
		}
		m.notifications.recoveryTimer = time.AfterFunc(delay, m.flushRecoveryNotifications)
	}
	m.mu.Unlock()
}

func (m *Manager) flushRecoveryNotifications() {
	now := time.Now().UTC()
	m.mu.Lock()
	if m.closing {
		m.notifications.recoveries = nil
		m.notifications.recoveryDropped = 0
		m.notifications.recoveryTimer = nil
		m.mu.Unlock()
		return
	}
	targets := m.notifications.recoveries
	dropped := m.notifications.recoveryDropped
	m.notifications.recoveries = nil
	m.notifications.recoveryDropped = 0
	m.notifications.recoveryTimer = nil
	window := m.settings.RecoveryMergeSeconds
	m.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	m.sendMessageAsync("AI Watch 测活恢复", recoveryMarkdown(now, window, targets, dropped))
}

func (m *Manager) sendMessageAsync(title, content string) {
	notifier, err := m.messageNotifier()
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	select {
	case m.notificationSlots <- struct{}{}:
		m.notificationWG.Add(1)
	default:
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	go func() {
		defer func() {
			<-m.notificationSlots
			m.notificationWG.Done()
		}()
		_ = notifier.Send(context.Background(), title, content)
	}()
}

func (m *Manager) statusSummaryMarkdown() string {
	jobs := m.List()
	sort.SliceStable(jobs, func(i, j int) bool {
		iActive := jobs[i].EndedAt == nil
		jActive := jobs[j].EndedAt == nil
		if iActive != jActive {
			return iActive
		}
		return jobs[i].StartedAt.After(jobs[j].StartedAt)
	})
	unique := make(map[string]domain.Job)
	order := make([]string, 0, min(len(jobs), maxNotificationTargets))
	active := 0
	for _, job := range jobs {
		if job.EndedAt == nil {
			active++
		}
		key := notificationTargetKey(job)
		if _, exists := unique[key]; exists || len(order) >= maxNotificationTargets {
			continue
		}
		unique[key] = notificationSnapshot(job)
		order = append(order, key)
	}
	lines := []string{
		"### 📊 AI Watch 状态汇总",
		"",
		fmt.Sprintf("🕒 %s", notificationTime(time.Now().UTC())),
		fmt.Sprintf("📌 当前任务：**%d** 个；已知目标：**%d** 个", active, len(order)),
		"",
	}
	if len(order) == 0 {
		lines = append(lines, "暂无任务状态。")
	}
	for _, key := range order {
		job := unique[key]
		lines = append(lines,
			fmt.Sprintf("#### %s %s", statusIcon(job), notificationTargetName(job)),
			fmt.Sprintf("%s；%s；阶段 %s", cliName(job.CLI), job.Status, job.Phase),
			fmt.Sprintf("尝试 **%d** 次；最近结果 %s；启动 %s", job.Attempts, fallbackStatus(job.LatestAttempt), notificationTime(job.StartedAt)),
			"")
	}
	if len(jobs) > len(order) {
		lines = append(lines, fmt.Sprintf("另有 %d 条历史/重复任务未展开。", len(jobs)-len(order)))
	}
	return strings.Join(lines, "\n")
}

func keepaliveSummaryMarkdown(now time.Time, elapsed time.Duration, successes int, targets map[string]notificationTarget, dropped int) string {
	lines := []string{
		"### 🟢 AI Watch 保活汇总", "",
		fmt.Sprintf("🕒 %s", notificationTime(now)),
		fmt.Sprintf("⏱️ 汇总周期：%s", notificationDuration(elapsed)),
		fmt.Sprintf("✅ 本周期保活成功：**%d** 次", successes), "",
	}
	for _, target := range sortedNotificationTargets(targets) {
		lines = append(lines,
			fmt.Sprintf("#### ✅ %s", notificationTargetName(target.Job)),
			fmt.Sprintf("%s；本周期成功 **%d** 次；累计尝试 %d 次", cliName(target.Job.CLI), target.Count, target.Job.Attempts), "")
	}
	return appendDropped(lines, dropped)
}

func probeProgressMarkdown(now time.Time, elapsed time.Duration, attempts int, targets map[string]notificationTarget, dropped int) string {
	lines := []string{
		"### 🟡 AI Watch 测活进度", "",
		fmt.Sprintf("🕒 %s", notificationTime(now)),
		fmt.Sprintf("⏱️ 汇总周期：%s", notificationDuration(elapsed)),
		fmt.Sprintf("🔎 本周期测活：**%d** 次；涉及 **%d** 个目标", attempts, len(targets)), "",
	}
	for _, target := range sortedNotificationTargets(targets) {
		lines = append(lines,
			fmt.Sprintf("#### ⚠️ %s", notificationTargetName(target.Job)),
			fmt.Sprintf("%s；本周期尝试 **%d** 次；最近结果 %s", cliName(target.Job.CLI), target.Count, fallbackStatus(target.Job.LatestAttempt)), "")
	}
	return appendDropped(lines, dropped)
}

func recoveryMarkdown(now time.Time, windowSeconds int, targets map[string]notificationTarget, dropped int) string {
	lines := []string{
		"### ✅ AI Watch 测活恢复", "",
		fmt.Sprintf("🕒 %s", notificationTime(now)),
		fmt.Sprintf("✅ 恢复供应商：**%d** 个", len(targets)),
		fmt.Sprintf("⏱️ 合并窗口：%s", notificationDuration(time.Duration(windowSeconds)*time.Second)), "",
	}
	for _, target := range sortedNotificationTargets(targets) {
		lines = append(lines,
			fmt.Sprintf("#### ✅ %s", notificationTargetName(target.Job)),
			fmt.Sprintf("%s；已恢复可用；探测 %d 次；耗时 %s", cliName(target.Job.CLI), target.Job.Attempts, notificationDuration(time.Duration(target.Job.ElapsedMillis)*time.Millisecond)), "")
	}
	return appendDropped(lines, dropped)
}

func addNotificationTarget(targets *map[string]notificationTarget, dropped *int, job domain.Job) {
	if *targets == nil {
		*targets = make(map[string]notificationTarget)
	}
	key := notificationTargetKey(job)
	value, exists := (*targets)[key]
	if !exists && len(*targets) >= maxNotificationTargets {
		(*dropped)++
		return
	}
	value.Job = notificationSnapshot(job)
	value.Count++
	(*targets)[key] = value
}

func notificationSnapshot(job domain.Job) domain.Job {
	job.MaskedKey = ""
	job.Target = sanitizeTarget(job.Target)
	return job
}

func notificationTargetKey(job domain.Job) string {
	id := job.ProviderID
	if id == "" {
		id = job.Target
	}
	return string(job.CLI) + "|" + id
}

func notificationTargetName(job domain.Job) string {
	for _, value := range []string{job.ProviderName, job.ProviderID, job.Target, "当前配置"} {
		if value != "" {
			return compactNotificationText(value)
		}
	}
	return "当前配置"
}

func sortedNotificationTargets(targets map[string]notificationTarget) []notificationTarget {
	values := make([]notificationTarget, 0, len(targets))
	for _, target := range targets {
		values = append(values, target)
	}
	sort.Slice(values, func(i, j int) bool {
		left := string(values[i].Job.CLI) + "|" + notificationTargetName(values[i].Job)
		right := string(values[j].Job.CLI) + "|" + notificationTargetName(values[j].Job)
		return left < right
	})
	return values
}

func appendDropped(lines []string, dropped int) string {
	if dropped > 0 {
		lines = append(lines, fmt.Sprintf("另有 %d 个目标因单次通知上限未展开。", dropped))
	}
	return strings.Join(lines, "\n")
}

func compactNotificationText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.NewReplacer("*", "", "`", "", "[", "", "]", "").Replace(value)
	runes := []rune(value)
	if len(runes) > 120 {
		return string(runes[:117]) + "..."
	}
	return value
}

func notificationTime(value time.Time) string {
	if value.IsZero() {
		return "未记录"
	}
	return value.In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02 15:04:05")
}

func notificationDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	if value < time.Second {
		return fmt.Sprintf("%dms", value.Milliseconds())
	}
	return fmt.Sprintf("%ds", (value+time.Second-1)/time.Second)
}

func cliName(cli domain.CLI) string {
	if cli == domain.CLIClaude {
		return "Claude Code"
	}
	return "Codex"
}

func fallbackStatus(status domain.AttemptStatus) string {
	if status == "" {
		return "未记录"
	}
	return string(status)
}

func statusIcon(job domain.Job) string {
	if job.EndedAt == nil || job.Status == domain.JobSuccess {
		return "🟢"
	}
	if job.Status == domain.JobStopped {
		return "⚪"
	}
	return "🔴"
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

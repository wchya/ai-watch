package notify

import (
	"ai-watch/internal/domain"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type DingTalk struct {
	URL    string
	Client *http.Client
}

func New(url string) *DingTalk {
	return &DingTalk{URL: url, Client: &http.Client{Timeout: 10 * time.Second}}
}
func (d *DingTalk) Configured() bool { return d != nil && d.URL != "" }
func (d *DingTalk) Notify(ctx context.Context, j domain.Job, a domain.AttemptStatus) error {
	if !d.Configured() {
		return nil
	}
	zone := time.FixedZone("CST", 8*60*60)
	tool := "Codex"
	if j.CLI == domain.CLIClaude {
		tool = "Claude"
	}
	result := "探活失败"
	state := "不可恢复错误"
	lastLabel := "最后失败时间"
	if a == domain.AttemptSuccess {
		result = "探活成功"
		state = "已恢复可用"
		lastLabel = "最后成功可用"
	}
	source := "当前 " + tool + " 配置"
	if j.ProviderID != "" {
		name := j.ProviderName
		if name == "" {
			name = "未命名 Provider"
		}
		source = fmt.Sprintf("CC Switch: %s (%s)", name, j.ProviderID)
	}
	end := time.Now().UTC()
	if j.EndedAt != nil {
		end = *j.EndedAt
	}
	duration := time.Duration(j.ElapsedMillis) * time.Millisecond
	text := fmt.Sprintf("%s %s\n状态：%s 探测次数：%d 总耗时：%s\n服务信息\n• 工具：%s\n• 脚本：ai-watch.sh\n• 配置来源：%s\n• 模型：%s\n• provider：%s\n连接信息\n• base_url：%s\n• apikey：%s\n时间\n• 探测开始：%s\n• %s：%s\n• 通知时间：%s",
		tool, result, state, j.Attempts, formatDuration(duration), tool, source, fallback(j.Model), fallback(j.Provider), fallback(j.Target), fallback(j.MaskedKey),
		j.StartedAt.In(zone).Format("2006-01-02 15:04:05"), lastLabel, end.In(zone).Format("2006-01-02 15:04:05"), time.Now().In(zone).Format("2006-01-02 15:04:05"))
	b, _ := json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": text}})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := d.Client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DingTalk returned %s", resp.Status)
	}
	var response struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("decode DingTalk response: %w", err)
	}
	if response.ErrCode != 0 {
		return fmt.Errorf("DingTalk error %d: %s", response.ErrCode, response.ErrMsg)
	}
	return nil
}

func fallback(v string) string {
	if v == "" {
		return "未提供"
	}
	return v
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%ds", (d+time.Second-1)/time.Second)
}

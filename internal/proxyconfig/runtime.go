package proxyconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPController struct {
	baseURL string
	client  *http.Client
}

func NewHTTPController(baseURL string, timeout time.Duration) *HTTPController {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPController{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), client: &http.Client{Timeout: timeout}}
}

func (c *HTTPController) Reload(ctx context.Context, path string) error {
	payload, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/configs?force=true", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("reload Mihomo configuration: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("reload Mihomo configuration: HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *HTTPController) GroupStatus(ctx context.Context, name string) (GroupStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/proxies/"+url.PathEscape(name), nil)
	if err != nil {
		return GroupStatus{}, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return GroupStatus{}, fmt.Errorf("read Mihomo proxy group: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return GroupStatus{}, fmt.Errorf("read Mihomo proxy group: HTTP %d", response.StatusCode)
	}
	var payload struct {
		All []string `json:"all"`
		Now string   `json:"now"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&payload); err != nil {
		return GroupStatus{}, errors.New("decode Mihomo proxy group")
	}
	nodeCount := 0
	for _, node := range payload.All {
		switch strings.ToUpper(strings.TrimSpace(node)) {
		case "", "DIRECT", "REJECT", "PASS", "COMPATIBLE":
		default:
			nodeCount++
		}
	}
	return GroupStatus{NodeCount: nodeCount, CurrentNode: payload.Now}, nil
}

type HTTPProxyTester struct {
	proxyURL string
	testURL  string
	timeout  time.Duration
}

func NewHTTPProxyTester(proxyURL, testURL string, timeout time.Duration) *HTTPProxyTester {
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	return &HTTPProxyTester{proxyURL: strings.TrimSpace(proxyURL), testURL: strings.TrimSpace(testURL), timeout: timeout}
}

func (t *HTTPProxyTester) Test(ctx context.Context) error {
	proxyURL, err := url.Parse(t.proxyURL)
	if err != nil || proxyURL.Host == "" {
		return errors.New("invalid proxy endpoint")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{Timeout: t.timeout, Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, t.testURL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("proxy test returned HTTP %d", response.StatusCode)
	}
	return nil
}

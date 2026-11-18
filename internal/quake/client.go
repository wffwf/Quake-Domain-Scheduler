// Package quake 实现 360Quake 搜索 API 客户端，支持多 Token 顺序切换与重试。
//
// 请求构造严格遵循参考样例 quake_probe.py：
//   - POST {base_url}，JSON body: {"query","start","size"}
//   - 请求头: Content-Type: application/json, X-QuakeToken: <token>
//   - 响应信封: {code, message, data[], meta}
//   - 分页仅使用 start(偏移) + size，绝不使用 scroll / scroll_id / cursor。
package quake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Token 一个可用的 Quake API Token。
type Token struct {
	Name  string // 显示名（用于日志/审计）
	Value string // token 值
}

// Meta 响应中的 meta 信息（字段为可选项，缺失时回退到记录数估算）。
type Meta struct {
	Pagination     *Pagination `json:"pagination"`
	ConsumedPoints int         `json:"consumed_points"`
}

// Pagination 分页元信息。
type Pagination struct {
	Count     int `json:"count"`
	PageIndex int `json:"page_index"`
	PageSize  int `json:"page_size"`
	Total     int `json:"total"`
}

type searchResponse struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    []json.RawMessage `json:"data"`
	Meta    json.RawMessage   `json:"meta"`
}

// Client Quake API 客户端。
type Client struct {
	BaseURL    string
	HTTP       *http.Client
	MaxRetries int
	Log        *slog.Logger
}

// New 构造客户端。
func New(baseURL string, timeoutSec, maxRetries int, log *slog.Logger) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP: &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
			// 不自动跟随重定向：Quake 鉴权失败可能 302 到 /quake/login，
			// 直接拿到 3xx/4xx 状态码更利于判定。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		MaxRetries: maxRetries,
		Log:        log,
	}
}

// SearchOnce 使用单个 Token 发起一次搜索请求。
func (c *Client) SearchOnce(token, query string, start, size int) ([]json.RawMessage, *Meta, error) {
	body, err := json.Marshal(map[string]any{"query": query, "start": start, "size": size})
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-QuakeToken", token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 先按 HTTP 状态码处理（401 响应体可能是非 JSON 的 "/quake/login"）。
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, fmt.Errorf("鉴权失败(401)：token 无效或已过期")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, fmt.Errorf("Quake 限流(429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var sr searchResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, nil, fmt.Errorf("响应不是合法 JSON: %s", truncate(string(raw), 300))
	}
	if sr.Code != 0 {
		return nil, nil, fmt.Errorf("接口返回异常 code=%d message=%s", sr.Code, truncate(sr.Message, 300))
	}
	if sr.Data == nil {
		sr.Data = []json.RawMessage{}
	}

	meta := &Meta{ConsumedPoints: len(sr.Data)}
	if len(sr.Meta) > 0 {
		var m Meta
		if err := json.Unmarshal(sr.Meta, &m); err == nil {
			if m.Pagination != nil {
				meta.Pagination = m.Pagination
			}
			if m.ConsumedPoints > 0 {
				meta.ConsumedPoints = m.ConsumedPoints
			}
		}
	}
	return sr.Data, meta, nil
}

// Search 按顺序使用多个 Token，逐个重试，直到成功；全部失败返回错误。
// 返回的 usedToken 用于审计日志记录。
func (c *Client) Search(tokens []Token, query string, start, size int) (data []json.RawMessage, meta *Meta, usedToken string, err error) {
	if len(tokens) == 0 {
		return nil, nil, "", fmt.Errorf("没有可用的 API Token")
	}
	var lastErr error
	for _, tk := range tokens {
		for attempt := 0; attempt <= c.MaxRetries; attempt++ {
			d, m, e := c.SearchOnce(tk.Value, query, start, size)
			if e == nil {
				return d, m, tk.Name, nil
			}
			lastErr = e
			if c.Log != nil {
				c.Log.Warn("Quake 请求失败", "token", tk.Name, "attempt", attempt+1, "query", query, "err", e.Error())
			}
			if attempt < c.MaxRetries {
				time.Sleep(time.Duration(300*(attempt+1)) * time.Millisecond)
			}
		}
		if c.Log != nil {
			c.Log.Warn("API Token 重试耗尽，切换下一个", "token", tk.Name)
		}
	}
	return nil, nil, "", fmt.Errorf("所有 API Token 均不可用: %w", lastErr)
}

// TestToken 校验单个 Token 是否有效（发一次 size=1 的查询）。
func (c *Client) TestToken(token string) error {
	_, _, err := c.SearchOnce(token, `domain: "example.com"`, 0, 1)
	return err
}

// CreditInfo 账号剩余点数信息。
type CreditInfo struct {
	Total          int `json:"total"`           // 剩余总积分
	MonthRemaining int `json:"month_remaining"` // 本月剩余积分
}

// userInfoData /api/v3/user/info 响应 data 中与点数相关的字段。
type userInfoData struct {
	Credit               int `json:"credit"`
	MonthRemainingCredit int `json:"month_remaining_credit"`
}

type userInfoResponse struct {
	Code    int          `json:"code"`
	Message string       `json:"message"`
	Data    userInfoData `json:"data"`
}

// userInfoURL 由搜索 base_url 推导用户信息端点（同源 /api/v3/user/info）。
func (c *Client) userInfoURL() string {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return "https://quake.360.net/api/v3/user/info"
	}
	u.Path = "/api/v3/user/info"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// GetCredit 查询单个 Token 的剩余点数（GET /api/v3/user/info）。
func (c *Client) GetCredit(token string) (CreditInfo, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.userInfoURL(), nil)
	if err != nil {
		return CreditInfo{}, err
	}
	req.Header.Set("X-QuakeToken", token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return CreditInfo{}, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CreditInfo{}, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return CreditInfo{}, fmt.Errorf("鉴权失败(401)：token 无效或已过期")
	}
	if resp.StatusCode != http.StatusOK {
		return CreditInfo{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var ur userInfoResponse
	if err := json.Unmarshal(raw, &ur); err != nil {
		return CreditInfo{}, fmt.Errorf("响应不是合法 JSON: %s", truncate(string(raw), 300))
	}
	if ur.Code != 0 {
		return CreditInfo{}, fmt.Errorf("接口返回异常 code=%d message=%s", ur.Code, truncate(ur.Message, 300))
	}
	return CreditInfo{Total: ur.Data.Credit, MonthRemaining: ur.Data.MonthRemainingCredit}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

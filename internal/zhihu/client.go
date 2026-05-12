package zhihu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"reverse-assassin/internal/config"
)

type Client struct {
	httpClient *http.Client
	limiter    *rate.Limiter
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		limiter:    rate.NewLimiter(rate.Limit(config.GlobalQPS), config.GlobalQPS),
	}
}

// sign 生成 HMAC-SHA256 签名
func (c *Client) sign(logID, extraInfo string) (timestamp, signature string) {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	signStr := fmt.Sprintf("app_key:%s|ts:%s|logid:%s|extra_info:%s",
		config.AppKey(), ts, logID, extraInfo)

	mac := hmac.New(sha256.New, []byte(config.AppSecret()))
	mac.Write([]byte(signStr))
	return ts, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// headers 构造请求头
func (c *Client) headers() map[string]string {
	logID := fmt.Sprintf("log_%d", time.Now().UnixNano())
	ts, sig := c.sign(logID, "")
	return map[string]string{
		"X-App-Key":    config.AppKey(),
		"X-Timestamp":  ts,
		"X-Log-Id":     logID,
		"X-Sign":       sig,
		"X-Extra-Info": "",
	}
}

// doGet 发送 GET 请求
func (c *Client) doGet(path string, params map[string]string, baseURL string) ([]byte, error) {
	c.limiter.Wait(context.Background())

	urlBase := baseURL
	if urlBase == "" {
		urlBase = config.ZhihuAPIBase
	}

	req, err := http.NewRequest("GET", urlBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}

	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// doPost 发送 POST 请求
func (c *Client) doPost(path string, body interface{}, baseURL string) ([]byte, error) {
	c.limiter.Wait(context.Background())

	urlBase := baseURL
	if urlBase == "" {
		urlBase = config.ZhihuAPIBase
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequest("POST", urlBase+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	for k, v := range c.headers() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

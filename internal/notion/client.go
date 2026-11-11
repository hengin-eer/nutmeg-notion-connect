// Package notion is a small read-only client for the Notion REST API.
package notion

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL = "https://api.notion.com/v1/"
	Version = "2022-06-28"
	// Notion allows an average of 3 requests per second per integration.
	minInterval = 350 * time.Millisecond
	// 429/5xx back off exponentially; 8 tries covers about 4 minutes of waiting.
	maxAttempts = 8
	// Network errors are usually short blips, so retry only a few times.
	maxNetRetries = 3
)

// Error is a non-retryable API error with a hint in Japanese.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string {
	hint := map[int]string{
		401: "キーが無効です。`nn auth login` で登録し直してください",
		403: "権限がありません（このインテグレーションは読み取り専用です）",
		404: "見つかりません。IDが違うか、インテグレーションに共有されていません",
	}[e.Status]
	return strings.TrimSpace(fmt.Sprintf("HTTP %d %s %s", e.Status, hint, e.Msg))
}

// IsStatus reports whether err is an API error with one of the given codes.
func IsStatus(err error, codes ...int) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	for _, c := range codes {
		if e.Status == c {
			return true
		}
	}
	return false
}

// Client throttles requests so callers never need to think about rate limits.
type Client struct {
	key  string
	http *http.Client
	last time.Time
}

func New(key string) *Client {
	return &Client{key: key, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Get(path string) (Obj, error) { return c.do(http.MethodGet, path, nil) }

func (c *Client) Post(path string, body any) (Obj, error) {
	return c.do(http.MethodPost, path, body)
}

func (c *Client) do(method, path string, body any) (Obj, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	netFails := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if wait := minInterval - time.Since(c.last); wait > 0 {
			time.Sleep(wait)
		}
		c.last = time.Now()
		req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Notion-Version", Version)
		req.Header.Set("Content-Type", "application/json")
		// Only a constant marker is stored: never URLs, headers, or credentials.
		if path := os.Getenv("NN_STATS_FILE"); path != "" {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, fmt.Errorf("open request stats: %w", err)
			}
			_, writeErr := f.WriteString("request\n")
			closeErr := f.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		res, err := c.http.Do(req)
		if err != nil {
			if netFails++; netFails <= maxNetRetries {
				time.Sleep(2 * time.Second)
				continue
			}
			return nil, fmt.Errorf("接続エラー: %w", err)
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		if res.StatusCode == 429 || res.StatusCode >= 500 {
			time.Sleep(retryAfter(res.Header.Get("Retry-After"), attempt))
			continue
		}
		if res.StatusCode >= 400 {
			var e struct{ Message string }
			_ = json.Unmarshal(data, &e)
			return nil, &Error{Status: res.StatusCode, Msg: e.Message}
		}
		var o Obj
		if err := json.Unmarshal(data, &o); err != nil {
			return nil, err
		}
		return o, nil
	}
	return nil, errors.New("再試行の上限に達しました（429/5xx が続きました）")
}

func retryAfter(h string, attempt int) time.Duration {
	if s, err := strconv.ParseFloat(h, 64); err == nil && s > 0 {
		return time.Duration(s * float64(time.Second))
	}
	return time.Duration(1<<attempt) * time.Second
}

// Stop ends Paginate early without an error.
var Stop = errors.New("stop")

// Paginate walks every page of a list endpoint. A nil body means GET.
func (c *Client) Paginate(path string, body Obj, fn func(Obj) error) error {
	cursor := ""
	for {
		var r Obj
		var err error
		if body == nil {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			u := path + sep + "page_size=100"
			if cursor != "" {
				u += "&start_cursor=" + cursor
			}
			r, err = c.Get(u)
		} else {
			b := Obj{"page_size": 100}
			for k, v := range body {
				b[k] = v
			}
			if cursor != "" {
				b["start_cursor"] = cursor
			}
			r, err = c.Post(path, b)
		}
		if err != nil {
			return err
		}
		for _, x := range Arr(r, "results") {
			o, _ := x.(map[string]any)
			if err := fn(o); err != nil {
				if errors.Is(err, Stop) {
					return nil
				}
				return err
			}
		}
		if !Bool(r, "has_more") {
			return nil
		}
		cursor = Str(r, "next_cursor")
	}
}

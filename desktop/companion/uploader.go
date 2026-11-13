package companion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	ServerURL   string
	DeviceToken string
	HTTP        *http.Client
}

func NewClient(serverURL, token string) *Client {
	return &Client{
		ServerURL:   strings.TrimRight(serverURL, "/"),
		DeviceToken: token,
		HTTP:        &http.Client{Timeout: 10 * time.Minute},
	}
}

type uploadResponse struct {
	Status    string `json:"status"`
	ReceiptID string `json:"receiptId"`
	Duplicate bool   `json:"duplicate"`
}

type APIError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server returned %d: %s", e.Status, e.Message)
}

func (c *Client) Register(ctx context.Context) (string, string, error) {
	body, err := json.Marshal(map[string]string{"appVersion": AppVersion, "os": runtime.GOOS})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ServerURL+"/v1/devices", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WoWCensusCompanion/"+AppVersion)
	var out struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := c.do(req, &out); err != nil {
		return "", "", err
	}
	if out.DeviceID == "" || out.Token == "" {
		return "", "", errors.New("server did not return a device token")
	}
	return out.DeviceID, out.Token, nil
}

func (c *Client) Upload(ctx context.Context, b *Bundle) (Receipt, error) {
	if c.ServerURL == "" || c.DeviceToken == "" {
		return Receipt{}, errors.New("not registered with the server yet")
	}
	m := b.Manifest
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.ServerURL+"/v1/uploads", bytes.NewReader(b.Zip))
	if err != nil {
		return Receipt{}, err
	}
	req.ContentLength = int64(len(b.Zip))
	req.Header.Set("Content-Type", "application/zip")
	req.Header.Set("Authorization", "Bearer "+c.DeviceToken)
	req.Header.Set("User-Agent", "WoWCensusCompanion/"+AppVersion)
	req.Header.Set("X-WoWCensus-Source-Id", m.SourceID)
	req.Header.Set("X-WoWCensus-Sha256", m.SHA256)
	req.Header.Set("X-WoWCensus-Zip-Sha256", b.ZipSHA)
	req.Header.Set("X-WoWCensus-Lua-Bytes", strconv.FormatInt(m.Bytes, 10))
	req.Header.Set("X-WoWCensus-Edition", m.Edition)
	req.Header.Set("X-WoWCensus-App-Version", m.AppVersion)

	var out uploadResponse
	if err := c.do(req, &out); err != nil {
		return Receipt{}, err
	}
	if out.ReceiptID == "" || out.Status == "" {
		return Receipt{}, errors.New("server response has no receipt")
	}
	return Receipt{
		SourceID: m.SourceID, SHA256: m.SHA256, Status: out.Status, ReceiptID: out.ReceiptID,
		Duplicate: out.Duplicate, Bytes: int64(len(b.Zip)), At: time.Now().UTC(),
	}, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		apiErr := &APIError{Status: resp.StatusCode, Message: e.Error}
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			apiErr.RetryAfter = time.Duration(secs) * time.Second
		}
		return apiErr
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unreadable server response: %w", err)
	}
	return nil
}

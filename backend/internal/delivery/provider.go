package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPProvider sends messages to a provider with the mock provider's API:
// POST {base}/sms or {base}/email with an Idempotency-Key header.
// It does not import package mockprovider; the contract is the HTTP API,
// as with a real provider (D-ARCH).
type HTTPProvider struct {
	base    string
	client  *http.Client
	timeout time.Duration
}

// NewHTTPProvider returns a provider client for baseURL. timeout bounds
// each request; a timed-out request is retried with the same key.
func NewHTTPProvider(baseURL string, timeout time.Duration) *HTTPProvider {
	return &HTTPProvider{
		base:    strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}},
		timeout: timeout,
	}
}

type smsBody struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

type emailBody struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Send implements Provider.
func (p *HTTPProvider) Send(ctx context.Context, m Message) SendResult {
	var body any = smsBody{To: m.To, Text: m.Text}
	if m.Channel == ChannelEmail {
		body = emailBody{To: m.To, Subject: m.Subject, Body: m.Body}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return SendResult{Err: fmt.Errorf("encode message: %w", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/"+string(m.Channel), bytes.NewReader(b))
	if err != nil {
		return SendResult{Err: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", m.IdempotencyKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return SendResult{Err: err}
	}
	defer resp.Body.Close()
	// Drain the body so the connection is reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return SendResult{StatusCode: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
}

// retryAfter parses a Retry-After header: seconds or an HTTP date.
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

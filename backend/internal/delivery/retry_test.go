package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	p := DefaultRetryPolicy()
	half := func() float64 { return 0.5 }
	job := func(attempts, throttles int) Job { return Job{Attempts: attempts, Throttles: throttles} }

	tests := []struct {
		name      string
		res       SendResult
		job       Job
		kind      Kind
		delay     time.Duration
		throttled bool
		errSub    string
	}{
		{"202 is sent", SendResult{StatusCode: 202}, job(1, 0), KindSent, 0, false, ""},
		{"200 is sent", SendResult{StatusCode: 200}, job(3, 0), KindSent, 0, false, ""},
		{"500 retries with backoff", SendResult{StatusCode: 500}, job(1, 0), KindRetry, time.Second, false, "http 500"},
		{"503 third attempt", SendResult{StatusCode: 503}, job(3, 0), KindRetry, 4 * time.Second, false, "http 503"},
		{"500 on the last attempt fails", SendResult{StatusCode: 500}, job(5, 0), KindFailed, 0, false, "gave up after 5 attempts"},
		{"timeout retries", SendResult{Err: context.DeadlineExceeded}, job(1, 0), KindRetry, time.Second, false, "timeout"},
		{"connection error retries", SendResult{Err: errors.New("dial tcp: connection refused")}, job(2, 0), KindRetry, 2 * time.Second, false, "connection error"},
		{"409 in progress retries", SendResult{StatusCode: 409}, job(1, 0), KindRetry, time.Second, false, "http 409"},
		{"408 retries", SendResult{StatusCode: 408}, job(1, 0), KindRetry, time.Second, false, "http 408"},
		{"429 waits Retry-After", SendResult{StatusCode: 429, RetryAfter: 3 * time.Second}, job(1, 0), KindRetry, 3 * time.Second, true, "http 429"},
		{"429 on the last attempt still waits", SendResult{StatusCode: 429, RetryAfter: time.Second}, job(5, 0), KindRetry, time.Second, true, "http 429"},
		{"429 without header uses base", SendResult{StatusCode: 429}, job(1, 0), KindRetry, 2 * time.Second, true, "http 429"},
		{"429 Retry-After capped", SendResult{StatusCode: 429, RetryAfter: time.Hour}, job(1, 0), KindRetry, 5 * time.Minute, true, "http 429"},
		{"too many 429s fail", SendResult{StatusCode: 429, RetryAfter: time.Second}, job(1, 19), KindFailed, 0, false, "rate limited 20 times"},
		{"400 is permanent", SendResult{StatusCode: 400}, job(1, 0), KindFailed, 0, false, "http 400"},
		{"422 is permanent", SendResult{StatusCode: 422}, job(1, 0), KindFailed, 0, false, "http 422"},
		{"404 is permanent", SendResult{StatusCode: 404}, job(1, 0), KindFailed, 0, false, "http 404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := p.Decide(tt.res, tt.job, half)
			if d.Kind != tt.kind || d.Delay != tt.delay || d.Throttled != tt.throttled || !strings.Contains(d.Error, tt.errSub) {
				t.Fatalf("got %+v, want kind %s delay %s throttled %v error ~%q", d, tt.kind, tt.delay, tt.throttled, tt.errSub)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	p := DefaultRetryPolicy()
	almostOne := func() float64 { return 0.999999 }
	zero := func() float64 { return 0 }
	// Ceilings: 2s, 4s, 8s, ... capped at 5 min.
	for attempt, ceiling := range map[int]time.Duration{
		1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 8: 256 * time.Second, 9: 5 * time.Minute, 40: 5 * time.Minute,
	} {
		top := p.Backoff(attempt, almostOne)
		if top >= ceiling || top < ceiling*99/100 {
			t.Errorf("attempt %d: top %s, ceiling %s", attempt, top, ceiling)
		}
		if got := p.Backoff(attempt, zero); got != 0 {
			t.Errorf("attempt %d: full jitter starts at 0, got %s", attempt, got)
		}
	}
}

func TestRetryAfterHeader(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	for v, want := range map[string]time.Duration{
		"":                              0,
		"1":                             time.Second,
		" 7 ":                           7 * time.Second,
		"-1":                            0,
		"soon":                          0,
		"Mon, 15 Jun 2026 12:00:30 GMT": 30 * time.Second,
		"Mon, 15 Jun 2026 11:00:00 GMT": 0, // in the past
	} {
		if got := retryAfter(v, now); got != want {
			t.Errorf("retryAfter(%q) = %s, want %s", v, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	j := Job{ID: 42, Channel: ChannelSMS, To: "+905551112233", FirstName: "", City: "İzmir",
		SMSText: "Merhaba {{first_name}}, {{ city }}'e özel!", EmailSubject: "{{first_name}}", EmailBody: "Selam"}
	m, err := render(j)
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "Merhaba Değerli Müşterimiz, İzmir'e özel!" || m.IdempotencyKey != "42" || m.To != j.To || m.Subject != "" {
		t.Fatalf("sms %+v", m)
	}
	j.Channel, j.FirstName = ChannelEmail, "Ayşe"
	if m, err = render(j); err != nil || m.Subject != "Ayşe" || m.Body != "Selam" || m.Text != "" {
		t.Fatalf("email %+v, %v", m, err)
	}
	j.EmailBody = "{{soyad}}"
	if _, err := render(j); err == nil {
		t.Fatal("unknown variable rendered")
	}
}

package translation

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryKind(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&statusError{code: 429}, "transient"},
		{&statusError{code: 503}, "transient"},
		{&statusError{code: 401}, ""},
		{&statusError{code: 400}, ""},
		{context.Canceled, ""},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), "timeout"},
		{errors.New("something else"), ""},
	}
	for _, c := range cases {
		if got := retryKind(c.err); got != c.want {
			t.Errorf("retryKind(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestOpenAIRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Привет"}}]}`)
	}))
	defer srv.Close()

	b, err := NewOpenAIBackend(BackendConfig{BaseURL: srv.URL, APIKey: "k"}, "Russian", func(string, ...interface{}) {})
	if err != nil {
		t.Fatal(err)
	}
	restore := setRetryDelay(time.Millisecond)
	defer restore()
	got, err := b.TranslateText("Hello")
	if err != nil || got != "Привет" {
		t.Fatalf("TranslateText = %q, %v; want a success after retries", got, err)
	}
	if calls.Load() != 3 {
		t.Errorf("server called %d times, want 3", calls.Load())
	}
}

func TestNoRetryWhenNothingListens(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	b, err := NewOpenAIBackend(BackendConfig{BaseURL: "http://" + addr, APIKey: "k"}, "Russian", func(string, ...interface{}) {})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := b.TranslateText("Hello"); err == nil {
		t.Fatal("expected an error")
	}
	if time.Since(start) > time.Second {
		t.Error("a refused connection was retried")
	}
}

func TestCancelStopsWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := withRetry(ctx, nil, func() (string, error) {
		calls++
		return "", &statusError{code: 503}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("fn called %d times, want 1 (the wait is cancelled)", calls)
	}
}

package translation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"
)

// statusError is a non-200 reply of a translation server.
type statusError struct {
	server     string
	code       int
	body       string
	retryAfter time.Duration
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s returned status %d: %s", e.server, e.code, e.body)
}

func newStatusError(server string, resp *http.Response, body []byte) *statusError {
	e := &statusError{server: server, code: resp.StatusCode, body: truncate(string(body), 500)}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		e.retryAfter = time.Duration(s) * time.Second
	}
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

const (
	retryTries        = 5 // rate limits and server errors
	retryTimeoutTries = 3 // timeouts: each attempt already waited the full timeout
	retryMaxDelay     = 60 * time.Second
)

var retryBaseDelay = 2 * time.Second

// setRetryDelay shortens the waits in tests.
func setRetryDelay(d time.Duration) (restore func()) {
	old := retryBaseDelay
	retryBaseDelay = d
	return func() { retryBaseDelay = old }
}

// retryKind says whether an error is worth another attempt: "" = no,
// "timeout" or "transient" = yes.
func retryKind(err error) string {
	var se *statusError
	if errors.As(err, &se) {
		switch se.code {
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
			return "transient"
		}
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return ""
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "" // nothing listens: the server is not running
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return "transient"
	}
	return ""
}

// withRetry calls fn until it succeeds, fails for good, runs out of attempts
// or ctx is done. Waits grow exponentially and honour Retry-After.
func withRetry(ctx context.Context, logFn LogFunc, fn func() (string, error)) (string, error) {
	delay := retryBaseDelay
	timeouts, others := 0, 0
	for {
		out, err := fn()
		if err == nil || ctx.Err() != nil {
			if ctx.Err() != nil && err != nil {
				return "", ctx.Err()
			}
			return out, err
		}
		switch retryKind(err) {
		case "timeout":
			timeouts++
			if timeouts >= retryTimeoutTries {
				return "", err
			}
		case "transient":
			others++
			if others >= retryTries {
				return "", err
			}
		default:
			return "", err
		}
		wait := delay
		var se *statusError
		if errors.As(err, &se) && se.retryAfter > 0 {
			wait = se.retryAfter
			if wait > retryMaxDelay {
				wait = retryMaxDelay
			}
		}
		if logFn != nil {
			logFn("    ⟳ %v — retrying in %s", err, wait.Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
		if delay > retryMaxDelay {
			delay = retryMaxDelay
		}
	}
}

package httputil

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// IsRetryableStatusCode reports whether HTTP status code should be retried.
func IsRetryableStatusCode(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

// IsRetryableTransportError reports whether transport error should be retried.
func IsRetryableTransportError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// SleepWithContext sleeps with cancellation support.
func SleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ReadBodyForError reads a bounded amount of response body for error messages.
func ReadBodyForError(body io.Reader) string {
	if body == nil {
		return ""
	}
	limited := io.LimitReader(body, 128*1024)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return ""
	}
	return string(payload)
}

// DiscardAndClose drains and closes response body.
func DiscardAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

// ClientForMode chooses a request client for streaming/non-streaming requests.
func ClientForMode(base *http.Client, stream bool, defaultTimeout time.Duration) *http.Client {
	if base == nil {
		if stream {
			return &http.Client{}
		}
		return &http.Client{Timeout: defaultTimeout}
	}
	if !stream {
		return base
	}

	clone := *base
	clone.Timeout = 0
	return &clone
}

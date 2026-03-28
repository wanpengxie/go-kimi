package httputil

import (
	"context"
	"errors"
	"net"
	"testing"
)

type stubNetError struct {
	msg     string
	timeout bool
}

func (e stubNetError) Error() string {
	return e.msg
}

func (e stubNetError) Timeout() bool {
	return e.timeout
}

func (e stubNetError) Temporary() bool {
	return !e.timeout
}

func TestIsRetryableTransportError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "timeout net error",
			err:  stubNetError{msg: "timeout", timeout: true},
			want: true,
		},
		{
			name: "non-timeout net error",
			err:  stubNetError{msg: "temporary failure", timeout: false},
			want: true,
		},
		{
			name: "regular error",
			err:  errors.New("permanent failure"),
			want: false,
		},
		{
			name: "dns resolution error",
			err: &net.DNSError{
				Err:        "no such host",
				Name:       "bad.invalid",
				IsNotFound: true,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IsRetryableTransportError(tc.err); got != tc.want {
				t.Fatalf("IsRetryableTransportError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

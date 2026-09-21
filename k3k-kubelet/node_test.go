package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/rancher/k3k/pkg/controller"
	"github.com/rancher/k3k/pkg/k3s"
)

// withFastBackoff shrinks the package retry backoff so the retry paths run in
// milliseconds instead of minutes.
func withFastBackoff(t *testing.T) {
	t.Helper()

	old := controller.Backoff
	controller.Backoff = wait.Backoff{
		Steps:    3,
		Duration: time.Millisecond,
		Factor:   1.0,
		Jitter:   0,
	}

	t.Cleanup(func() { controller.Backoff = old })
}

// A first-attempt failure with an error satisfying wait.Interrupted (such as a
// wrapped context.DeadlineExceeded from the HTTP client) makes retry.OnError
// return its recorded-but-nil last retriable error: no error, no certificate.
// Before the nil guard this dereferenced a nil certificate and crashed the
// kubelet with a SIGSEGV.
func TestRequestServingCertSwallowedTimeout(t *testing.T) {
	withFastBackoff(t)

	_, err := requestServingCert(func() (*tls.Certificate, error) {
		return nil, fmt.Errorf("Head \"https://server:6443\": %w", context.DeadlineExceeded)
	})
	if err == nil {
		t.Fatal("expected an error for a timed-out certificate request, got nil")
	}

	if !strings.Contains(err.Error(), "no certificate returned") {
		t.Fatalf("expected the nil-certificate guard error, got: %v", err)
	}
}

// ErrServerNotReady arrives wrapped (fmt.Errorf %w chains); the retriable
// check must use errors.Is, not equality, or the retry never happens.
func TestRequestServingCertRetriesWrappedServerNotReady(t *testing.T) {
	withFastBackoff(t)

	calls := 0
	want := &tls.Certificate{}

	got, err := requestServingCert(func() (*tls.Certificate, error) {
		calls++
		if calls < 3 {
			return nil, fmt.Errorf("server returned 503: %w", k3s.ErrServerNotReady)
		}

		return want, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != want {
		t.Fatal("returned certificate is not the one the getter produced")
	}

	if calls != 3 {
		t.Fatalf("expected 3 attempts (2 retries), got %d", calls)
	}
}

// Non-retriable errors must surface to the caller wrapped, not be retried.
func TestRequestServingCertNonRetriableError(t *testing.T) {
	withFastBackoff(t)

	boom := errors.New("boom")
	calls := 0

	_, err := requestServingCert(func() (*tls.Certificate, error) {
		calls++
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected the getter error wrapped, got: %v", err)
	}

	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a non-retriable error, got %d", calls)
	}
}

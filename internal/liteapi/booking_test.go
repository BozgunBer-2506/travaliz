package liteapi

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type bookingTransport func(*http.Request) (*http.Response, error)

func (f bookingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBookingRejectsProductionUnknownKeysAndDisabledFlag(t *testing.T) {
	for _, key := range []string{"prod_forbidden", "unknown", ""} {
		t.Setenv("LITEAPI_API_KEY", key)
		t.Setenv("LITEAPI_SANDBOX_BOOKING_ENABLED", "true")
		c := NewClient()
		c.HTTPClient.Transport = bookingTransport(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe key reached network"); return nil, nil })
		if _, err := c.Prebook("offer"); err != ErrSandboxDisabled {
			t.Fatal("prebook production guard bypassed")
		}
		if _, err := c.Book("prebook", "ref", Guest{}); err != ErrSandboxDisabled {
			t.Fatal("book production guard bypassed")
		}
	}
	t.Setenv("LITEAPI_API_KEY", "sand_test")
	t.Setenv("LITEAPI_SANDBOX_BOOKING_ENABLED", "false")
	if NewClient().SandboxBookingEnabled() {
		t.Fatal("booking enabled by default")
	}
}

func TestPrebookMustExplicitlyConfirmSandboxAndSelectedOffer(t *testing.T) {
	t.Setenv("LITEAPI_API_KEY", "sandbox_test")
	t.Setenv("LITEAPI_SANDBOX_BOOKING_ENABLED", "true")
	for _, body := range []string{`{"data":{"offerId":"offer"},"sandbox":false}`, `{"data":{"offerId":"different"},"sandbox":true}`, `{"data":{"offerId":"offer"}}`} {
		c := NewClient()
		c.HTTPClient.Transport = bookingTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := c.Prebook("offer"); err == nil {
			t.Fatal("unattested offer accepted")
		}
	}
}

func TestIncompleteProviderQuoteIsRejected(t *testing.T) {
	if _, err := (Prebook{}).Quote("Hotel"); err == nil {
		t.Fatal("missing price/availability/terms accepted")
	}
}

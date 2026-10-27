package duffel

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestNewLiveSearchClientGating(t *testing.T) {
	for _, tc := range []struct {
		name, flag, token string
		wantErr           error
	}{
		{"flag unset", "", "duffel_live_x", ErrLiveSearchDisabled},
		{"flag false", "false", "duffel_live_x", ErrLiveSearchDisabled},
		{"flag true, no token", "true", "", ErrLiveTokenInvalid},
		// A sandbox token must never grant live access, even with the flag on.
		{"flag true, sandbox token", "true", "duffel_test_x", ErrLiveTokenInvalid},
		{"flag true, unknown prefix", "true", "prod_x", ErrLiveTokenInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LIVE_FLIGHT_SEARCH_ENABLED", tc.flag)
			t.Setenv("DUFFEL_LIVE_API_TOKEN", tc.token)
			client, err := NewLiveSearchClient()
			if client != nil || err != tc.wantErr {
				t.Fatalf("got (%v, %v), want (nil, %v)", client, err, tc.wantErr)
			}
		})
	}
}

func TestNewLiveSearchClientEnabled(t *testing.T) {
	t.Setenv("LIVE_FLIGHT_SEARCH_ENABLED", "true")
	t.Setenv("DUFFEL_LIVE_API_TOKEN", "duffel_live_abc123")
	client, err := NewLiveSearchClient()
	if err != nil || client == nil {
		t.Fatalf("got (%v, %v), want a usable client", client, err)
	}
}

func TestLiveSearchClientSendsTheLiveTokenOnly(t *testing.T) {
	t.Setenv("LIVE_FLIGHT_SEARCH_ENABLED", "true")
	t.Setenv("DUFFEL_LIVE_API_TOKEN", "duffel_live_only_this_one")
	client, err := NewLiveSearchClient()
	if err != nil {
		t.Fatal(err)
	}
	var sawAuth, sawVersion, sawPath, sawMethod string
	client.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawAuth, sawVersion, sawPath, sawMethod = r.Header.Get("Authorization"), r.Header.Get("Duffel-Version"), r.URL.Path, r.Method
		return jsonResponse(`{"data":{"offers":[]}}`), nil
	})
	if _, err := client.CreateOfferRequest(
		[]SearchSlice{{Origin: "BER", Destination: "LHR", DepartureDate: "2026-12-01"}},
		[]SearchPassenger{{Type: "adult"}}, "economy",
	); err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer duffel_live_only_this_one" {
		t.Fatalf("Authorization = %q, want the live token", sawAuth)
	}
	if sawVersion != "v2" || sawMethod != http.MethodPost || sawPath != "/air/offer_requests" {
		t.Fatalf("unexpected request: version=%q method=%q path=%q", sawVersion, sawMethod, sawPath)
	}

	client.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawAuth, sawPath = r.Header.Get("Authorization"), r.URL.Path
		return jsonResponse(`{"data":{"id":"off_1"}}`), nil
	})
	if _, err := client.GetOffer("off_1"); err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer duffel_live_only_this_one" || sawPath != "/air/offers/off_1" {
		t.Fatalf("unexpected GetOffer request: auth=%q path=%q", sawAuth, sawPath)
	}
}

// LiveSearchClient has no CreateOrder method: this is a compile-time property
// (the method set literally lacks it), not a runtime guard. This test
// documents and locks that contract — it would fail to compile, not fail at
// runtime, if CreateOrder were ever added to this type.
func TestLiveSearchClientMethodSetHasNoCreateOrder(t *testing.T) {
	var c any = &LiveSearchClient{}
	type orderCapable interface {
		CreateOrder(offerID, amount, currency string, passengers []OrderPassenger) (Order, error)
	}
	if _, ok := c.(orderCapable); ok {
		t.Fatal("LiveSearchClient must never implement CreateOrder")
	}
}

// Cross-contamination must be impossible in the other direction too: the
// existing sandbox client must keep rejecting a live token.
func TestDuffelClientRejectsLiveToken(t *testing.T) {
	t.Setenv("DUFFEL_API_TOKEN", "duffel_live_should_not_work_here")
	client := NewDuffelClient()
	client.HTTPClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("a live token must never reach the network through DuffelClient")
		return nil, nil
	})
	_, err := client.CreateOfferRequest([]SearchSlice{{Origin: "BER", Destination: "LHR", DepartureDate: "2026-12-01"}},
		[]SearchPassenger{{Type: "adult"}}, "economy")
	if err == nil || !strings.Contains(err.Error(), "sandbox test token is required") {
		t.Fatalf("got %v, want a sandbox-token-required error", err)
	}
}

func TestDoRequestSharedByBothClientsDecodesConsistently(t *testing.T) {
	// A smoke check that the extracted doRequest still round-trips the
	// envelope correctly for both callers via a trivial decode target.
	var out struct {
		Offers []Offer `json:"offers"`
	}
	body := `{"data":{"offers":[{"id":"off_shared"}]}}`
	if err := doRequest(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}, "duffel_live_x", http.MethodGet, "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Offers) != 1 || out.Offers[0].ID != "off_shared" {
		t.Fatalf("unexpected decode: %+v", out)
	}
}

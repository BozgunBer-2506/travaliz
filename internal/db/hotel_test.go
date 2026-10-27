package db

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type hotelTransport func(*http.Request) (*http.Response, error)

func (f hotelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHotelStoreUsesDurableUniqueInsertAndAtomicOwnerScopedClaim(t *testing.T) {
	d, _ := Open("https://supabase.test", "server-only-test-key")
	calls := 0
	d.client.Transport = hotelTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("apikey") != "server-only-test-key" || r.URL.Path != "/rest/v1/hotel_sandbox_checkouts" {
			t.Fatal("wrong trust boundary")
		}
		switch calls {
		case 1:
			if r.Method != "POST" || r.URL.Query().Get("on_conflict") != "user_id,offer_hash" || !strings.Contains(r.Header.Get("Prefer"), "resolution=ignore-duplicates") {
				t.Fatal("non-atomic checkout reservation")
			}
		case 2:
			if r.Method != "GET" || r.URL.Query().Get("user_id") != "eq.owner" || r.URL.Query().Get("offer_hash") != "eq.hash" {
				t.Fatal("conflict lookup missing ownership")
			}
		case 3:
			if r.Method != "PATCH" || r.URL.Query().Get("user_id") != "eq.owner" || r.URL.Query().Get("id") != "eq.checkout" || r.URL.Query().Get("status") != "eq.quoted" || r.Header.Get("Prefer") != "return=representation" {
				t.Fatal("claim missing atomic state/owner predicate")
			}
		default:
			t.Fatal("unexpected storage retry")
		}
		body := `[]`
		if calls == 2 {
			body = `[{"id":"checkout","user_id":"owner","status":"quoted"}]`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	r, fresh, err := d.ReserveHotel("owner", "hash")
	if err != nil || fresh || r.ID != "checkout" {
		t.Fatal("existing checkout not reused")
	}
	_, won, err := d.UpdateHotel("checkout", "owner", "quoted", "booking", nil)
	if err != nil || won {
		t.Fatal("zero updated rows treated as a successful lock")
	}
	if calls != 3 {
		t.Fatalf("unexpected storage operations: %d", calls)
	}
}

func TestHotelStoreDoesNotExposeDatabaseErrorBodies(t *testing.T) {
	d, _ := Open("https://supabase.test", "server-only-test-key")
	d.client.Transport = hotelTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`PRIVATE ERROR WITH CREDENTIALS`))}, nil
	})
	_, err := d.GetHotel("id", "owner")
	if err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("database error details leaked")
	}
}

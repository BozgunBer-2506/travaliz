package liteapi

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Explicit opt-in only: checks existing sandbox data with GET requests. A
// private fixture is produced by the external sandbox capability check, never
// committed. This test cannot create a prebook, booking or payment.
func TestLiveSandboxReadContracts(t *testing.T) {
	if os.Getenv("TRAVALIZ_TEST_LIVE") != "true" {
		t.Skip("explicit sandbox read-contract opt-in required")
	}
	c := NewClient()
	if !c.SandboxBookingEnabled() {
		t.Fatal("recognized sandbox key and explicit booking flag required")
	}
	f, err := os.Open(os.Getenv("TRAVALIZ_PREBOOK_FIXTURE"))
	if err != nil {
		t.Fatal("private prebook fixture unavailable")
	}
	defer f.Close()
	var fixture struct {
		Prebook struct {
			Data    Prebook `json:"data"`
			Sandbox bool    `json:"sandbox"`
		} `json:"prebook"`
	}
	if json.NewDecoder(f).Decode(&fixture) != nil || !fixture.Prebook.Sandbox {
		t.Fatal("invalid sandbox fixture")
	}
	p := fixture.Prebook.Data
	name, err := c.HotelName(p.HotelID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := p.Quote(name)
	if err != nil {
		t.Fatal(err)
	}
	after, err := c.GetPrebook(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := after.Quote(name)
	if err == nil && quote.Digest() != before.Digest() {
		left, _ := json.Marshal(before)
		right, _ := json.Marshal(quote)
		var a, b map[string]any
		json.Unmarshal(left, &a)
		json.Unmarshal(right, &b)
		for key, value := range a {
			if !reflect.DeepEqual(value, b[key]) {
				t.Logf("GET prebook differs in quote field: %s", key)
			}
		}
	}
	if err != nil || quote.Digest() != before.Digest() {
		t.Fatal("retrieved prebook changed the verified quote")
	}
	_, found, err := c.FindBooking("00000000-0000-4000-8000-000000000000", p.ID, quote)
	if err != nil || found {
		t.Fatal("sandbox recovery lookup contract failed")
	}
}

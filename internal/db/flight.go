package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

// FlightCheckout is a durable lock row keyed by (user, offer), gating every
// CreateOrder attempt for that pair. It carries just enough state to make a
// repeated or concurrent /book request idempotent; the booking itself is
// still stored in the generic bookings table as before.
type FlightCheckout struct {
	ID               string    `json:"id"`
	UserID           string    `json:"user_id"`
	OfferID          string    `json:"offer_id"`
	Status           string    `json:"status"`
	OrderID          string    `json:"order_id,omitempty"`
	BookingReference string    `json:"booking_reference,omitempty"`
	BookingRef       string    `json:"booking_ref,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type FlightStore interface {
	ReserveFlight(userID, offerID string) (*FlightCheckout, bool, error)
	GetFlight(id, userID string) (*FlightCheckout, error)
	UpdateFlight(id, userID, from, to string, fields map[string]any) (*FlightCheckout, bool, error)
}

func (d *DB) ReserveFlight(userID, offerID string) (*FlightCheckout, bool, error) {
	row := map[string]any{"id": uuid.NewString(), "user_id": userID, "offer_id": offerID, "status": "pending"}
	rows, err := d.flightRequest(http.MethodPost, "?on_conflict=user_id,offer_id", row, true)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 1 {
		return &rows[0], true, nil
	}
	rows, err = d.flightRequest(http.MethodGet, "?user_id=eq."+url.QueryEscape(userID)+"&offer_id=eq."+url.QueryEscape(offerID)+"&limit=1", nil, false)
	if err != nil || len(rows) != 1 {
		return nil, false, errors.New("flight checkout lock could not be verified")
	}
	return &rows[0], false, nil
}

func (d *DB) GetFlight(id, userID string) (*FlightCheckout, error) {
	rows, err := d.flightRequest(http.MethodGet, "?id=eq."+url.QueryEscape(id)+"&user_id=eq."+url.QueryEscape(userID)+"&limit=1", nil, false)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// PostgreSQL evaluates the state predicate and update atomically, exactly
// like UpdateHotel: only the request that receives a returned row is
// allowed to call Duffel's CreateOrder for this lock.
func (d *DB) UpdateFlight(id, userID, from, to string, fields map[string]any) (*FlightCheckout, bool, error) {
	body := map[string]any{"status": to, "updated_at": time.Now().UTC()}
	for _, key := range []string{"order_id", "booking_reference", "booking_ref"} {
		if value, ok := fields[key]; ok {
			body[key] = value
		}
	}
	rows, err := d.flightRequest(http.MethodPatch, "?id=eq."+url.QueryEscape(id)+"&user_id=eq."+url.QueryEscape(userID)+"&status=eq."+url.QueryEscape(from), body, false)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return &rows[0], true, nil
}

func (d *DB) flightRequest(method, path string, body any, reserve bool) ([]FlightCheckout, error) {
	if d == nil {
		return nil, errors.New("flight checkout database is unavailable")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("invalid flight checkout data")
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, d.base+"/flight_sandbox_checkouts"+path, reader)
	if err != nil {
		return nil, errors.New("invalid flight checkout request")
	}
	req.Header.Set("apikey", d.apiKey)
	req.Header.Set("Content-Type", "application/json")
	prefer := "return=representation"
	if reserve {
		prefer += ",resolution=ignore-duplicates"
	}
	req.Header.Set("Prefer", prefer)
	client := *d.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("flight checkout storage outcome is unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("flight checkout storage is unavailable; verify migration and permissions")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, errors.New("incomplete flight checkout storage response")
	}
	var rows []FlightCheckout
	if json.Unmarshal(b, &rows) != nil {
		return nil, errors.New("invalid flight checkout storage response")
	}
	return rows, nil
}

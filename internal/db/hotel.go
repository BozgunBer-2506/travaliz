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
	"travel-proxy-service/internal/liteapi"
)

type HotelCheckout struct {
	ID                    string         `json:"id"`
	UserID                string         `json:"user_id"`
	OfferHash             string         `json:"offer_hash"`
	Status                string         `json:"status"`
	PrebookID             string         `json:"prebook_id,omitempty"`
	Quote                 *liteapi.Quote `json:"quote,omitempty"`
	Guest                 *liteapi.Guest `json:"guest,omitempty"`
	BookingID             string         `json:"booking_id,omitempty"`
	HotelConfirmationCode string         `json:"hotel_confirmation_code,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
}

type HotelStore interface {
	ReserveHotel(userID, offerHash string) (*HotelCheckout, bool, error)
	GetHotel(id, userID string) (*HotelCheckout, error)
	UpdateHotel(id, userID, from, to string, fields map[string]any) (*HotelCheckout, bool, error)
	ListHotels(userID string) ([]HotelCheckout, error)
}

func (d *DB) ReserveHotel(userID, offerHash string) (*HotelCheckout, bool, error) {
	row := map[string]any{"id": uuid.NewString(), "user_id": userID, "offer_hash": offerHash, "status": "prebooking"}
	rows, err := d.hotelRequest(http.MethodPost, "?on_conflict=user_id,offer_hash", row, true)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 1 {
		return &rows[0], true, nil
	}
	rows, err = d.hotelRequest(http.MethodGet, "?user_id=eq."+url.QueryEscape(userID)+"&offer_hash=eq."+url.QueryEscape(offerHash)+"&limit=1", nil, false)
	if err != nil || len(rows) != 1 {
		return nil, false, errors.New("checkout reservation could not be verified")
	}
	return &rows[0], false, nil
}

func (d *DB) GetHotel(id, userID string) (*HotelCheckout, error) {
	rows, err := d.hotelRequest(http.MethodGet, "?id=eq."+url.QueryEscape(id)+"&user_id=eq."+url.QueryEscape(userID)+"&limit=1", nil, false)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// PostgreSQL evaluates the state predicate and update atomically. Only the
// request that receives a returned row is allowed to send a provider book.
func (d *DB) UpdateHotel(id, userID, from, to string, fields map[string]any) (*HotelCheckout, bool, error) {
	body := map[string]any{"status": to, "updated_at": time.Now().UTC()}
	for _, key := range []string{"prebook_id", "quote", "guest", "booking_id", "hotel_confirmation_code"} {
		if value, ok := fields[key]; ok {
			body[key] = value
		}
	}
	rows, err := d.hotelRequest(http.MethodPatch, "?id=eq."+url.QueryEscape(id)+"&user_id=eq."+url.QueryEscape(userID)+"&status=eq."+url.QueryEscape(from), body, false)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	return &rows[0], true, nil
}

func (d *DB) ListHotels(userID string) ([]HotelCheckout, error) {
	return d.hotelRequest(http.MethodGet, "?user_id=eq."+url.QueryEscape(userID)+"&status=in.(booking,unknown,confirmed_sandbox)&order=created_at.desc", nil, false)
}

func (d *DB) hotelRequest(method, path string, body any, reserve bool) ([]HotelCheckout, error) {
	if d == nil {
		return nil, errors.New("checkout database is unavailable")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, errors.New("invalid checkout data")
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, d.base+"/hotel_sandbox_checkouts"+path, reader)
	if err != nil {
		return nil, errors.New("invalid checkout request")
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
		return nil, errors.New("checkout storage outcome is unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("checkout storage is unavailable; verify migration and permissions")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, errors.New("incomplete checkout storage response")
	}
	var rows []HotelCheckout
	if json.Unmarshal(b, &rows) != nil {
		return nil, errors.New("invalid checkout storage response")
	}
	return rows, nil
}

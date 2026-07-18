package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DB struct {
	base   string
	apiKey string
	client *http.Client
}

type Booking struct {
	ID         int64     `json:"id,omitempty"`
	Ref        string    `json:"ref"`
	Type       string    `json:"type"`
	FirstName  string    `json:"first_name"`
	LastName   string    `json:"last_name"`
	Email      string    `json:"email"`
	Phone      string    `json:"phone"`
	FromCode   string    `json:"from_code"`
	ToCode     string    `json:"to_code"`
	Airline    string    `json:"airline"`
	DepartTime string    `json:"depart_time"`
	ArriveTime string    `json:"arrive_time"`
	Duration   string    `json:"duration"`
	Stops      int       `json:"stops"`
	HotelName  string    `json:"hotel_name"`
	Checkin    string    `json:"checkin"`
	Checkout   string    `json:"checkout"`
	Price      float64   `json:"price"`
	Currency   string    `json:"currency"`
	CardLast4  string    `json:"card_last4"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
}

func Open(supabaseURL, apiKey string) (*DB, error) {
	if supabaseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("SUPABASE_URL and SUPABASE_SECRET_KEY are required")
	}
	return &DB{
		base:   strings.TrimRight(supabaseURL, "/") + "/rest/v1",
		apiKey: apiKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (d *DB) do(method, path string, body interface{}) ([]byte, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, d.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", d.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Prefer", "return=representation")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("supabase %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func (d *DB) CreateBooking(b *Booking) (string, error) {
	b.Ref = generateRef()
	_, err := d.do(http.MethodPost, "/bookings", b)
	if err != nil {
		return "", fmt.Errorf("insert booking: %w", err)
	}
	return b.Ref, nil
}

func (d *DB) GetBookingsByEmail(email string) ([]*Booking, error) {
	path := "/bookings?email=eq." + url.QueryEscape(email) + "&order=created_at.desc"
	data, err := d.do(http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("query bookings: %w", err)
	}
	var bookings []*Booking
	if err := json.Unmarshal(data, &bookings); err != nil {
		return nil, err
	}
	if bookings == nil {
		bookings = []*Booking{}
	}
	return bookings, nil
}

func (d *DB) GetBookingByRef(ref string) (*Booking, error) {
	path := "/bookings?ref=eq." + url.QueryEscape(ref) + "&limit=1"
	data, err := d.do(http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("query booking: %w", err)
	}
	var list []*Booking
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("booking not found")
	}
	return list[0], nil
}

func (d *DB) DeleteBooking(ref, email string) error {
	path := "/bookings?ref=eq." + url.QueryEscape(ref) + "&email=eq." + url.QueryEscape(email)
	_, err := d.do(http.MethodDelete, path, nil)
	return err
}

func generateRef() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var sb strings.Builder
	sb.WriteString("TM-")
	for i := 0; i < 6; i++ {
		sb.WriteByte(chars[r.Intn(len(chars))])
	}
	return sb.String()
}

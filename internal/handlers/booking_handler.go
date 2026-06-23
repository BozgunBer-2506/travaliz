package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"travel-proxy-service/internal/db"
)

type BookingRequest struct {
	Type       string  `json:"type"`
	FirstName  string  `json:"firstName"`
	LastName   string  `json:"lastName"`
	Email      string  `json:"email"`
	Phone      string  `json:"phone"`
	CardNumber string  `json:"cardNumber"`
	CardExpiry string  `json:"cardExpiry"`
	CardCVV    string  `json:"cardCvv"`
	FromCode   string  `json:"fromCode"`
	ToCode     string  `json:"toCode"`
	Airline    string  `json:"airline"`
	DepartTime string  `json:"departTime"`
	ArriveTime string  `json:"arriveTime"`
	Duration   string  `json:"duration"`
	Stops      int     `json:"stops"`
	HotelName  string  `json:"hotelName"`
	Checkin    string  `json:"checkin"`
	Checkout   string  `json:"checkout"`
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
}

type BookingResponse struct {
	Ref   string `json:"ref"`
	Error string `json:"error,omitempty"`
}

func (h *TravelHandler) BookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(BookingResponse{Error: "method not allowed"})
		return
	}

	var req BookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(BookingResponse{Error: "invalid request body"})
		return
	}

	if err := validateBooking(&req); err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(BookingResponse{Error: err.Error()})
		return
	}

	cardLast4 := last4(req.CardNumber)

	booking := &db.Booking{
		Type:       req.Type,
		FirstName:  strings.TrimSpace(req.FirstName),
		LastName:   strings.TrimSpace(req.LastName),
		Email:      strings.ToLower(strings.TrimSpace(req.Email)),
		Phone:      strings.TrimSpace(req.Phone),
		FromCode:   req.FromCode,
		ToCode:     req.ToCode,
		Airline:    req.Airline,
		DepartTime: req.DepartTime,
		ArriveTime: req.ArriveTime,
		Duration:   req.Duration,
		Stops:      req.Stops,
		HotelName:  req.HotelName,
		Checkin:    req.Checkin,
		Checkout:   req.Checkout,
		Price:      req.Price,
		Currency:   req.Currency,
		CardLast4:  cardLast4,
	}

	ref, err := h.DB.CreateBooking(booking)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(BookingResponse{Error: "booking failed, please try again"})
		return
	}

	json.NewEncoder(w).Encode(BookingResponse{Ref: ref})

	go sendB2BWebhook(booking, ref)
}

func (h *TravelHandler) MyBookingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	email := r.URL.Query().Get("email")
	if email == "" || !strings.Contains(email, "@") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "valid email required"})
		return
	}
	bookings, err := h.DB.GetBookingsByEmail(strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to fetch bookings"})
		return
	}
	if bookings == nil {
		bookings = []*db.Booking{}
	}
	json.NewEncoder(w).Encode(bookings)
}

func sendB2BWebhook(b *db.Booking, ref string) {
	webhookURL := os.Getenv("B2B_WEBHOOK_URL")
	secret := os.Getenv("B2B_WEBHOOK_SECRET")
	if webhookURL == "" || secret == "" {
		return
	}

	customer := map[string]any{
		"name":  strings.TrimSpace(b.FirstName + " " + b.LastName),
		"email": b.Email,
		"phone": b.Phone,
	}

	payload := map[string]any{
		"secret":          secret,
		"externalOrderId": ref,
		"customer":        customer,
	}

	switch b.Type {
	case "flight":
		payload["flight"] = map[string]any{
			"departureAirport": b.FromCode,
			"arrivalAirport":   b.ToCode,
			"airline":          b.Airline,
			"flightNumber":     b.Airline,
			"departureDate":    b.DepartTime,
			"arrivalDate":      b.ArriveTime,
			"price":            b.Price,
			"flightClass":      "economy",
			"passengerCount":   1,
		}
	case "hotel":
		payload["hotel"] = map[string]any{
			"hotelName":  b.HotelName,
			"city":       b.ToCode,
			"checkIn":    b.Checkin,
			"checkOut":   b.Checkout,
			"totalPrice": b.Price,
		}
	default:
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhook marshal error: %v", err)
		return
	}

	resp, err := http.Post(webhookURL+"/webhooks/travaliz", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("webhook send error: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("B2B webhook sent for %s: HTTP %d", ref, resp.StatusCode)
}

func validateBooking(req *BookingRequest) error {
	switch {
	case strings.TrimSpace(req.FirstName) == "":
		return fmt.Errorf("First name is required")
	case strings.TrimSpace(req.LastName) == "":
		return fmt.Errorf("Last name is required")
	case !strings.Contains(req.Email, "@"):
		return fmt.Errorf("Valid email is required")
	case len(strings.TrimSpace(req.Phone)) < 7:
		return fmt.Errorf("Valid phone number is required")
	case len(digitsOnly(req.CardNumber)) < 13:
		return fmt.Errorf("Valid card number is required")
	case len(req.CardExpiry) < 5:
		return fmt.Errorf("Card expiry is required (MM/YY)")
	case len(req.CardCVV) < 3:
		return fmt.Errorf("CVV is required")
	case req.Price <= 0:
		return fmt.Errorf("Invalid price")
	}
	return nil
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func last4(cardNumber string) string {
	d := digitsOnly(cardNumber)
	if len(d) >= 4 {
		return d[len(d)-4:]
	}
	return "****"
}

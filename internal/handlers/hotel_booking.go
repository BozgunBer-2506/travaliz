package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"travel-proxy-service/internal/auth"
	"travel-proxy-service/internal/db"
	"travel-proxy-service/internal/liteapi"
)

type hotelResult struct {
	CheckoutID   string         `json:"checkoutId"`
	Ref          string         `json:"ref"`
	Status       string         `json:"status"`
	Quote        *liteapi.Quote `json:"quote,omitempty"`
	QuoteHash    string         `json:"quoteHash,omitempty"`
	BookingID    string         `json:"hotelBookingId,omitempty"`
	Confirmation string         `json:"hotelConfirmationCode,omitempty"`
}

func hotelReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func hotelError(w http.ResponseWriter, status int, message string) {
	hotelReply(w, status, BookingResponse{Error: message})
}

func (h *TravelHandler) hotelClient() *liteapi.Client {
	if h.HotelClient != nil {
		return h.HotelClient
	}
	return liteapi.NewClient()
}

func (h *TravelHandler) hotelStore() db.HotelStore {
	if h.HotelStore != nil {
		return h.HotelStore
	}
	return h.DB
}

func checkoutResult(row *db.HotelCheckout) hotelResult {
	result := hotelResult{CheckoutID: row.ID, Ref: row.ID, Status: row.Status, Quote: row.Quote, BookingID: row.BookingID, Confirmation: row.HotelConfirmationCode}
	if row.Quote != nil {
		result.QuoteHash = row.Quote.Digest()
	}
	if result.Status == "booking" {
		result.Status = "unknown"
	}
	return result
}

func (h *TravelHandler) HotelPrebookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		hotelError(w, 405, "method not allowed")
		return
	}
	user, err := auth.SubjectFromRequest(r)
	if err != nil {
		hotelError(w, 401, "unauthorized")
		return
	}
	if _, err = auth.EmailFromRequest(r); err != nil {
		hotelError(w, 401, "unauthorized")
		return
	}
	client := h.hotelClient()
	if !client.SandboxBookingEnabled() {
		hotelError(w, 409, "Hotel sandbox booking is not enabled. No reservation or payment was made.")
		return
	}
	var input struct {
		OfferID string `json:"offerId"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input) != nil || len(input.OfferID) == 0 || len(input.OfferID) > 512<<10 || strings.TrimSpace(input.OfferID) != input.OfferID {
		hotelError(w, 422, "Please select a valid hotel offer.")
		return
	}
	hash := sha256.Sum256([]byte(input.OfferID))
	store := h.hotelStore()
	row, fresh, err := store.ReserveHotel(user, hex.EncodeToString(hash[:]))
	if err != nil {
		hotelError(w, 503, "Hotel checkout storage is unavailable. No reservation was made.")
		return
	}
	if !fresh {
		if row.Status == "failed" {
			hotelError(w, 409, "This offer cannot be booked. Please search again.")
			return
		}
		status := 200
		if row.Status == "prebooking" {
			status = 202
		}
		hotelReply(w, status, checkoutResult(row))
		return
	}
	prebook, err := client.Prebook(input.OfferID)
	if err != nil {
		store.UpdateHotel(row.ID, user, "prebooking", "failed", nil)
		hotelError(w, 502, "The hotel offer could not be verified. No booking request was sent; please search again.")
		return
	}
	name, err := client.HotelName(prebook.HotelID)
	if err != nil {
		store.UpdateHotel(row.ID, user, "prebooking", "failed", nil)
		hotelError(w, 502, "Hotel details could not be verified. Please search again.")
		return
	}
	quote, err := prebook.Quote(name)
	if err != nil {
		store.UpdateHotel(row.ID, user, "prebooking", "failed", nil)
		hotelError(w, 409, "Complete pricing, occupancy, cancellation terms or sandbox payment could not be verified. Please search again.")
		return
	}
	row, saved, err := store.UpdateHotel(row.ID, user, "prebooking", "quoted", map[string]any{"prebook_id": prebook.ID, "quote": quote})
	if err != nil || !saved {
		hotelError(w, 503, "The verified quote could not be saved. No booking request was sent.")
		return
	}
	hotelReply(w, 200, checkoutResult(row))
}

func (h *TravelHandler) bookHotel(w http.ResponseWriter, r *http.Request, input *BookingRequest) {
	client := h.hotelClient()
	if !client.SandboxBookingEnabled() {
		hotelError(w, 409, "Hotel booking is not available yet. No reservation was made and no payment was taken.")
		return
	}
	user, err := auth.SubjectFromRequest(r)
	if err != nil {
		hotelError(w, 401, "unauthorized")
		return
	}
	if _, err = uuid.Parse(input.HotelCheckoutID); err != nil {
		hotelError(w, 422, "A verified hotel checkout is required.")
		return
	}
	store := h.hotelStore()
	row, err := store.GetHotel(input.HotelCheckoutID, user)
	if err != nil {
		hotelError(w, 503, "Checkout storage is unavailable. No new booking request was sent.")
		return
	}
	if row == nil {
		hotelError(w, 404, "Checkout not found.")
		return
	}
	if row.Status != "quoted" {
		h.replyHotelState(w, row)
		return
	}
	if row.Quote == nil || !input.AcceptQuote || input.QuoteHash != row.Quote.Digest() {
		hotelError(w, 409, "Review and accept the verified price and cancellation terms first.")
		return
	}
	if time.Since(row.CreatedAt) > 10*time.Minute {
		hotelError(w, 409, "This checkout has expired. Please search again.")
		return
	}
	guest := liteapi.Guest{FirstName: strings.TrimSpace(input.FirstName), LastName: strings.TrimSpace(input.LastName), Email: input.Email}
	if guest.FirstName == "" || guest.LastName == "" || len(guest.FirstName) > 100 || len(guest.LastName) > 100 {
		hotelError(w, 422, "Enter the lead guest's first and last name.")
		return
	}
	prebook, err := client.GetPrebook(row.PrebookID)
	if err != nil {
		hotelError(w, 409, "The prebook could not be verified. No booking request was sent.")
		return
	}
	quote, err := prebook.Quote(row.Quote.HotelName)
	if err != nil || quote.Digest() != input.QuoteHash {
		hotelError(w, 409, "The hotel's price or terms changed. Please search again and review the new offer.")
		return
	}
	claimed, won, err := store.UpdateHotel(row.ID, user, "quoted", "booking", map[string]any{"guest": guest})
	if err != nil {
		hotelError(w, 503, "Booking was not started because the durable lock could not be verified.")
		return
	}
	if !won {
		current, err := store.GetHotel(row.ID, user)
		if err != nil || current == nil {
			hotelError(w, 503, "Booking state is unavailable. Do not place another reservation.")
			return
		}
		h.replyHotelState(w, current)
		return
	}
	booking, bookErr := client.Book(claimed.PrebookID, claimed.ID, guest)
	if bookErr != nil || (booking.PrebookID != "" && booking.PrebookID != claimed.PrebookID) ||
		(booking.ClientReference != "" && booking.ClientReference != claimed.ID) || (booking.HotelID != "" && booking.HotelID != quote.HotelID) || booking.Price != quote.Price || booking.Currency != quote.Currency {
		store.UpdateHotel(claimed.ID, user, "booking", "unknown", nil)
		claimed.Status = "unknown"
		h.replyHotelState(w, claimed)
		return
	}
	saved, ok, err := store.UpdateHotel(claimed.ID, user, "booking", "confirmed_sandbox", map[string]any{"booking_id": booking.ID, "hotel_confirmation_code": booking.HotelConfirmationCode})
	if err != nil || !ok {
		claimed.Status = "unknown"
		h.replyHotelState(w, claimed)
		return
	}
	hotelReply(w, 200, checkoutResult(saved))
}

func (h *TravelHandler) replyHotelState(w http.ResponseWriter, row *db.HotelCheckout) {
	if row.Status == "failed" {
		hotelError(w, 409, "This offer could not be booked. Please search again.")
		return
	}
	status := 200
	if row.Status == "booking" || row.Status == "unknown" || row.Status == "prebooking" {
		status = 202
	}
	hotelReply(w, status, checkoutResult(row))
}

func (h *TravelHandler) HotelStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		hotelError(w, 405, "method not allowed")
		return
	}
	user, err := auth.SubjectFromRequest(r)
	if err != nil {
		hotelError(w, 401, "unauthorized")
		return
	}
	client := h.hotelClient()
	if !client.SandboxBookingEnabled() {
		hotelError(w, 409, "Hotel sandbox booking is not enabled.")
		return
	}
	id := r.URL.Query().Get("id")
	if _, err = uuid.Parse(id); err != nil {
		hotelError(w, 404, "Checkout not found.")
		return
	}
	row, err := h.hotelStore().GetHotel(id, user)
	if err != nil {
		hotelError(w, 503, "Checkout storage is unavailable.")
		return
	}
	if row == nil {
		hotelError(w, 404, "Checkout not found.")
		return
	}
	if (row.Status == "booking" || row.Status == "unknown") && row.Quote != nil {
		booking, found, err := client.FindBooking(row.ID, row.PrebookID, *row.Quote)
		if err == nil && found {
			saved, ok, err := h.hotelStore().UpdateHotel(row.ID, user, row.Status, "confirmed_sandbox", map[string]any{"booking_id": booking.ID, "hotel_confirmation_code": booking.HotelConfirmationCode})
			if err == nil && ok {
				row = saved
			}
		}
	}
	h.replyHotelState(w, row)
}

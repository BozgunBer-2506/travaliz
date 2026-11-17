package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"travel-proxy-service/internal/auth"
	"travel-proxy-service/internal/db"
	"travel-proxy-service/internal/duffel"
)

// FlightPassengerInput is one traveller's personal details for a flight
// booking. BornOn must be consistent with the age the passenger was searched
// under (see bookFlightViaDuffel), since that age set the offer's fare.
type FlightPassengerInput struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	BornOn    string `json:"bornOn"`
	Gender    string `json:"gender"`
	Title     string `json:"title"`
}

type BookingRequest struct {
	HotelCheckoutID string `json:"hotelCheckoutId"`
	QuoteHash       string `json:"quoteHash"`
	AcceptQuote     bool   `json:"acceptQuote"`
	Type            string `json:"type"`
	OfferID         string `json:"offerId"`
	FirstName       string `json:"firstName"`
	LastName        string `json:"lastName"`
	Email           string `json:"email"`
	Phone           string `json:"phone"`
	BornOn          string `json:"bornOn"`
	Gender          string `json:"gender"`
	Title           string `json:"title"`
	// Passengers carries one entry per traveller on a multi-passenger flight
	// offer, in the same adults-then-children order the search request used.
	// Omitted (or empty) for the legacy single-adult shape, which falls back
	// to the top-level FirstName/LastName/BornOn/Gender/Title above.
	Passengers []FlightPassengerInput `json:"passengers,omitempty"`
	FromCode   string                 `json:"fromCode"`
	ToCode     string                 `json:"toCode"`
	Airline    string                 `json:"airline"`
	DepartTime string                 `json:"departTime"`
	ArriveTime string                 `json:"arriveTime"`
	Duration   string                 `json:"duration"`
	Stops      int                    `json:"stops"`
	HotelName  string                 `json:"hotelName"`
	Checkin    string                 `json:"checkin"`
	Checkout   string                 `json:"checkout"`
	Price      float64                `json:"price"`
	Currency   string                 `json:"currency"`
	// Slices is the itinerary the customer was shown, one entry per offer
	// slice. It is compared against the offer from GetOffer before any
	// order is created and is never used as booking data itself.
	Slices []BookingSlice `json:"slices,omitempty"`
}

// BookingSlice identifies one displayed slice by its first departure airport,
// final arrival airport and exact departing_at value from the search offer.
type BookingSlice struct {
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
	DepartingAt string `json:"departingAt"`
}

// errItineraryMismatch means the offer no longer describes the itinerary the
// customer saw, so no order may be created from it.
var errItineraryMismatch = errors.New("itinerary mismatch")

// verifyOfferItinerary checks every slice of the freshly loaded offer against
// the itinerary the customer was shown. Each slice must be complete. A client
// that sends no slices (a page rendered before multi-slice support) may only
// book a single-slice offer, so a round-trip or multi-city order can never be
// placed while the customer saw just one leg.
func verifyOfferItinerary(offer duffel.Offer, shown []BookingSlice) error {
	if len(shown) == 0 {
		if len(offer.Slices) > 1 {
			return errItineraryMismatch
		}
		return nil
	}
	if len(shown) != len(offer.Slices) {
		return errItineraryMismatch
	}
	for i, slice := range offer.Slices {
		if len(slice.Segments) == 0 {
			return errItineraryMismatch
		}
		first, last := slice.Segments[0], slice.Segments[len(slice.Segments)-1]
		if first.Origin.IATACode == "" || last.Destination.IATACode == "" || first.DepartingAt == "" || last.ArrivingAt == "" ||
			!strings.EqualFold(first.Origin.IATACode, strings.TrimSpace(shown[i].Origin)) ||
			!strings.EqualFold(last.Destination.IATACode, strings.TrimSpace(shown[i].Destination)) ||
			first.DepartingAt != strings.TrimSpace(shown[i].DepartingAt) {
			return errItineraryMismatch
		}
	}
	return nil
}

// applyOfferItinerary overwrites a flight booking row's route fields with
// the server-loaded offer when it has several slices, so My Bookings shows
// the whole itinerary instead of whichever single leg the client sent.
// Single-slice offers keep the existing one-way behaviour unchanged.
func applyOfferItinerary(b *db.Booking, offer duffel.Offer) {
	if len(offer.Slices) < 2 {
		return
	}
	legs := make([]db.ItineraryLeg, 0, len(offer.Slices))
	stops := 0
	for _, slice := range offer.Slices {
		if len(slice.Segments) == 0 {
			continue
		}
		first, last := slice.Segments[0], slice.Segments[len(slice.Segments)-1]
		duration := ""
		if hours, minutes, err := parseFlightDuration(slice.Duration); err == nil {
			duration = fmt.Sprintf("%dh %dm", hours, minutes)
		}
		legs = append(legs, db.ItineraryLeg{
			FromCode: first.Origin.IATACode, ToCode: last.Destination.IATACode,
			DepartTime: first.DepartingAt, ArriveTime: last.ArrivingAt,
			Duration: duration, Airline: first.MarketingCarrier.Name, Stops: len(slice.Segments) - 1,
		})
		stops += len(slice.Segments) - 1
	}
	if len(legs) == 0 {
		return
	}
	b.Itinerary = legs
	b.FromCode, b.ToCode = legs[0].FromCode, legs[len(legs)-1].ToCode
	b.DepartTime, b.ArriveTime = legs[0].DepartTime, legs[len(legs)-1].ArriveTime
	b.Airline, b.Duration, b.Stops = legs[0].Airline, "", stops
}

type BookingResponse struct {
	Ref    string `json:"ref"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

var e164Phone = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// ageAt computes age in whole years as of asOf, the same convention used for
// the age bracket a passenger was searched under. Age must be assessed as of
// the flight's departure date, not the booking date: a child searched at age
// 11 can turn 12 between booking and departure, which must not flip their
// fare bracket. See travelReferenceDate.
func ageAt(bornOn string, asOf time.Time) (int, error) {
	d, err := time.Parse("2006-01-02", bornOn)
	if err != nil {
		return 0, err
	}
	age := asOf.Year() - d.Year()
	if asOf.Month() < d.Month() || (asOf.Month() == d.Month() && asOf.Day() < d.Day()) {
		age--
	}
	if age < 0 {
		return 0, fmt.Errorf("date of birth is in the future")
	}
	return age, nil
}

// travelReferenceDate returns the local departure date of the offer's first
// slice, straight from Duffel's GetOffer response, never the client-supplied
// request fields. Duffel sends departing_at as airport-local time without an
// offset ("2006-01-02T15:04:05"); RFC3339 is accepted too. Only the calendar
// date is used, so ages are assessed on the local departure day. It never
// falls back to today: a missing or unreadable date is an error, because
// assessing ages as of the booking date can mis-bracket a child whose
// birthday falls before departure.
func travelReferenceDate(offer duffel.Offer) (time.Time, error) {
	if len(offer.Slices) == 0 || len(offer.Slices[0].Segments) == 0 {
		return time.Time{}, errors.New("offer has no departure date")
	}
	raw := offer.Slices[0].Segments[0].DepartingAt
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339} {
		if d, err := time.Parse(layout, raw); err == nil {
			return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("offer departure date %q is not readable", raw)
}

// matchPassengersToOffer pairs each submitted passenger with an offer
// passenger ID. It never trusts offer.Passengers' array order to match the
// submitted order: adults are interchangeable and matched as a group, and
// each non-adult is matched to a specific offer slot by its exact searched
// age (consuming that slot), since age is the only identifying field Duffel
// returns for an anonymous, age-based passenger. asOf is the age reference
// date (the offer's own departure date), not the booking date.
func matchPassengersToOffer(form []FlightPassengerInput, offer []duffel.OfferPassenger, asOf time.Time) ([]duffel.OrderPassenger, error) {
	if len(form) != len(offer) {
		return nil, fmt.Errorf("this offer has %d passenger(s); %d were submitted", len(offer), len(form))
	}
	type entry struct {
		index int
		p     FlightPassengerInput
		age   int
	}
	var adults, nonAdults []entry
	for i, p := range form {
		age, err := ageAt(p.BornOn, asOf)
		if err != nil {
			return nil, fmt.Errorf("passenger %d has an invalid date of birth", i+1)
		}
		if age >= 18 {
			adults = append(adults, entry{i, p, age})
		} else {
			nonAdults = append(nonAdults, entry{i, p, age})
		}
	}
	var adultSlots []duffel.OfferPassenger
	var childSlots []duffel.OfferPassenger
	for _, op := range offer {
		if op.Age == nil {
			adultSlots = append(adultSlots, op)
		} else {
			childSlots = append(childSlots, op)
		}
	}
	if len(adults) != len(adultSlots) {
		return nil, fmt.Errorf("this offer expects %d adult passenger(s); %d were submitted as adults", len(adultSlots), len(adults))
	}
	result := make([]duffel.OrderPassenger, len(form))
	for i, a := range adults {
		result[a.index] = duffel.OrderPassenger{ID: adultSlots[i].ID, GivenName: a.p.FirstName, FamilyName: a.p.LastName, BornOn: a.p.BornOn, Gender: a.p.Gender, Title: a.p.Title}
	}
	remaining := append([]duffel.OfferPassenger(nil), childSlots...)
	for _, c := range nonAdults {
		matched := -1
		for j, op := range remaining {
			if op.Age != nil && *op.Age == c.age {
				matched = j
				break
			}
		}
		if matched == -1 {
			return nil, fmt.Errorf("passenger %d's date of birth does not match any searched child age on this offer", c.index+1)
		}
		op := remaining[matched]
		remaining = append(remaining[:matched], remaining[matched+1:]...)
		result[c.index] = duffel.OrderPassenger{ID: op.ID, GivenName: c.p.FirstName, FamilyName: c.p.LastName, BornOn: c.p.BornOn, Gender: c.p.Gender, Title: c.p.Title}
	}
	return result, nil
}

// phoneExample is a real, dialable number (live-verified against Duffel's
// own validation, unlike the digit-count check above). The previous example,
// +491234567, matched e164Phone but was itself rejected by Duffel as an
// invalid phone number — so it was never a safe thing to show users.
const phoneExample = "+4915112345678"

func (h *TravelHandler) BookHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(BookingResponse{Error: "method not allowed"})
		return
	}

	jwtEmail, authErr := auth.EmailFromRequest(r)
	if authErr != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(BookingResponse{Error: "unauthorized"})
		return
	}

	var req BookingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(BookingResponse{Error: "invalid request body"})
		return
	}

	req.Email = jwtEmail

	if req.Type == "flight" {
		h.bookFlightViaDuffel(w, r, &req)
		return
	}

	if req.Type == "hotel" {
		h.bookHotel(w, r, &req)
		return
	}

	if err := validateBooking(&req); err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(BookingResponse{Error: err.Error()})
		return
	}

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
		CardLast4:  "", // Placeholder requests do not collect card data.
	}

	ref, err := h.DB.CreateBooking(booking)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(BookingResponse{Error: "booking failed, please try again"})
		return
	}

	json.NewEncoder(w).Encode(BookingResponse{Ref: ref})

	if os.Getenv("B2B_WEBHOOK_ENABLED") == "true" {
		go sendB2BWebhook(booking, ref)
	}
}

// flightOrderStaleAfter bounds how long a lock may sit at "ordering" before
// a later request is allowed to treat it the same as "unknown" (read-only
// reconciliation only, never a second CreateOrder call). Without this, a
// request whose process dies mid-CreateOrder -- the host killing a
// serverless function before duffel.orderCreateTimeout elapses is one way
// this could happen, see that constant's comment -- would leave the lock
// stuck at "ordering" forever, since nothing would ever run the deferred
// transition out of it. Set comfortably above orderCreateTimeout.
const flightOrderStaleAfter = 150 * time.Second

// bookFlightViaDuffel creates a real sandbox order. The offer's own price is
// always used for payment; a client-supplied price is never trusted.
//
// Concurrency and retries: a durable flight_sandbox_checkouts row, keyed by
// (user, offer), is reserved before any provider call and moved through its
// states with a conditional UPDATE (status = from), exactly like the
// existing hotel checkout flow in hotel_booking.go. CreateOrder is called
// from exactly one state, "pending", and so is called at most once per
// lock, ever -- this does not assume Duffel's Idempotency-Key header
// prevents a duplicate order on a repeat call, and never relies on it to
// justify calling CreateOrder a second time (see duffel.CreateOrder's own
// doc comment). Every other state (concurrent "ordering", "unknown" after a
// timeout, a stale "ordering" left behind by a dead process, "order_placed"
// after a successful order whose local write failed, "failed", or the
// terminal "needs_review") is handled without ever calling CreateOrder
// again: either answered from the lock's own cached state, resolved purely
// locally (finalizeFlightOrder, no Duffel call), or reconciled through a
// read-only, best-effort lookup (duffel.ListOrdersByOfferID) whose filter
// is NOT confirmed against Duffel's real API from this codebase -- an
// inconclusive result there always routes to "needs_review", never to a
// fresh CreateOrder call.
func (h *TravelHandler) bookFlightViaDuffel(w http.ResponseWriter, r *http.Request, req *BookingRequest) {
	phone := strings.TrimSpace(req.Phone)
	if !strings.Contains(req.Email, "@") {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(BookingResponse{Error: "A valid email is required"})
		return
	}
	if !e164Phone.MatchString(phone) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(BookingResponse{Error: "Please enter your phone number in international format, e.g. " + phoneExample})
		return
	}

	// The legacy single-adult shape carries its one passenger's details at
	// the request's top level; a multi-passenger request carries them in
	// Passengers, one entry per traveller, adults first then children, in
	// the same order the search request built them in.
	passengers := req.Passengers
	if len(passengers) == 0 {
		passengers = []FlightPassengerInput{{
			FirstName: req.FirstName, LastName: req.LastName,
			BornOn: req.BornOn, Gender: req.Gender, Title: req.Title,
		}}
	}
	for i := range passengers {
		passengers[i].FirstName = strings.TrimSpace(passengers[i].FirstName)
		passengers[i].LastName = strings.TrimSpace(passengers[i].LastName)
		if passengers[i].FirstName == "" || passengers[i].LastName == "" ||
			passengers[i].BornOn == "" || passengers[i].Gender == "" || passengers[i].Title == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(BookingResponse{Error: "Name, date of birth, gender and title are required for every passenger"})
			return
		}
	}

	offerID := strings.TrimSpace(req.OfferID)
	if offerID == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(BookingResponse{Error: "A flight offer is required"})
		return
	}
	if len(req.Slices) > maxFlightSlices {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(BookingResponse{Error: "Too many flights in this itinerary"})
		return
	}
	user, authErr := auth.SubjectFromRequest(r)
	if authErr != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(BookingResponse{Error: "unauthorized"})
		return
	}

	store := h.flightStore()
	lock, fresh, lockErr := store.ReserveFlight(user, offerID)
	if lockErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(BookingResponse{Error: "Booking lock is unavailable. No order was created."})
		return
	}

	client := duffel.NewDuffelClient()
	reconcile := false
	if !fresh {
		switch lock.Status {
		case "confirmed_sandbox":
			json.NewEncoder(w).Encode(BookingResponse{Ref: lock.BookingRef, Status: "confirmed_sandbox"})
			return
		case "order_placed":
			// Duffel already confirmed this order; only the local write is
			// missing. No Duffel call is made on this path.
			offer, err := client.GetOffer(offerID)
			if err != nil || offer.ID == "" {
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(BookingResponse{Error: "The confirmed order's details could not be reloaded. Please try again; no new order will be created."})
				return
			}
			price, _ := strconv.ParseFloat(offer.TotalAmount, 64)
			resp, code := h.finalizeFlightOrder(offer, store, user, lock, passengers[0], req, phone, offer.TotalCurrency, price, lock.OrderID, lock.BookingReference)
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(resp)
			return
		case "failed":
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(BookingResponse{Error: "This offer could not be booked. Please search again."})
			return
		case "needs_review":
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(BookingResponse{Error: "This booking's outcome could not be confirmed automatically. Please check My Bookings or contact support before trying again."})
			return
		case "unknown":
			reconcile = true
		case "ordering":
			if time.Since(lock.UpdatedAt) <= flightOrderStaleAfter {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(BookingResponse{Error: "A booking request for this offer is already in progress. Check My Bookings before retrying."})
				return
			}
			reconcile = true
		}
	}

	if reconcile {
		// A read-only, best-effort lookup. See ListOrdersByOfferID's doc
		// comment: its filter is not confirmed against Duffel's real API,
		// so anything other than exactly one matching order is treated as
		// inconclusive, never as "safe to create a new order".
		orders, listErr := client.ListOrdersByOfferID(offerID)
		offer, offerErr := client.GetOffer(offerID)
		matched := -1
		if listErr == nil && offerErr == nil && offer.ID != "" {
			for i, o := range orders {
				if o.ID != "" && o.TotalAmount == offer.TotalAmount && o.TotalCurrency == offer.TotalCurrency {
					matched = i
				}
			}
		}
		if listErr != nil || offerErr != nil || len(orders) == 0 || matched == -1 || len(orders) > 1 {
			store.UpdateFlight(lock.ID, user, lock.Status, "needs_review", nil)
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(BookingResponse{Error: "This booking's outcome could not be confirmed automatically. Please check My Bookings or contact support before trying again."})
			return
		}
		claimed, won, lockErr := store.UpdateFlight(lock.ID, user, lock.Status, "order_placed", map[string]any{
			"order_id": orders[matched].ID, "booking_reference": orders[matched].BookingReference,
		})
		if lockErr != nil || !won {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(BookingResponse{Error: "A booking request for this offer is already in progress. Check My Bookings before retrying."})
			return
		}
		price, _ := strconv.ParseFloat(offer.TotalAmount, 64)
		resp, code := h.finalizeFlightOrder(offer, store, user, claimed, passengers[0], req, phone, offer.TotalCurrency, price, claimed.OrderID, claimed.BookingReference)
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Only a genuinely fresh lock reaches here, so this is the sole place
	// CreateOrder is ever called for a given lock.
	claimed, won, lockErr := store.UpdateFlight(lock.ID, user, "pending", "ordering", nil)
	if lockErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(BookingResponse{Error: "Booking lock is unavailable. No order was created."})
		return
	}
	if !won {
		current, _ := store.GetFlight(lock.ID, user)
		if current != nil && current.Status == "confirmed_sandbox" {
			json.NewEncoder(w).Encode(BookingResponse{Ref: current.BookingRef, Status: "confirmed_sandbox"})
			return
		}
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(BookingResponse{Error: "A booking request for this offer is already in progress. Check My Bookings before retrying."})
		return
	}

	offer, err := client.GetOffer(offerID)
	if err != nil || offer.ID == "" || len(offer.Passengers) == 0 {
		// No provider decision was made yet; let a resubmission (e.g. after a
		// fresh search) retry the lock cleanly instead of burning it.
		store.UpdateFlight(claimed.ID, user, "ordering", "pending", nil)
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(BookingResponse{Error: "Fiyat güncellendi veya teklif doldu, tekrar arayın"})
		return
	}
	if itineraryErr := verifyOfferItinerary(offer, req.Slices); itineraryErr != nil {
		store.UpdateFlight(claimed.ID, user, "ordering", "pending", nil)
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(BookingResponse{Error: "This itinerary has changed or is no longer available. Please search again. No order was created."})
		return
	}
	departure, dateErr := travelReferenceDate(offer)
	if dateErr != nil {
		store.UpdateFlight(claimed.ID, user, "ordering", "pending", nil)
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(BookingResponse{Error: "This offer's departure date could not be verified. Please search again. No order was created."})
		return
	}
	orderPassengers, matchErr := matchPassengersToOffer(passengers, offer.Passengers, departure)
	if matchErr != nil {
		store.UpdateFlight(claimed.ID, user, "ordering", "pending", nil)
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(BookingResponse{Error: matchErr.Error()})
		return
	}
	for i := range orderPassengers {
		orderPassengers[i].Email = req.Email
		orderPassengers[i].PhoneNumber = phone
	}

	order, orderErr := client.CreateOrder(offer.ID, offer.TotalAmount, offer.TotalCurrency, orderPassengers, claimed.ID)
	if orderErr != nil {
		if !errors.Is(orderErr, duffel.ErrTimeout) {
			store.UpdateFlight(claimed.ID, user, "ordering", "failed", nil)
			// Live-verified: Duffel rejects a phone number that merely matches our
			// own length check (e.g. the old UI example, +491234567) with this exact
			// code. Code/Type are short machine-readable enums from Duffel's own
			// docs, not free text, so surfacing this one is safe; every other
			// order-creation failure keeps the generic message below.
			message := "Rezervasyon tamamlanamadı, tekrar deneyin"
			var apiErr *duffel.APIError
			if errors.As(orderErr, &apiErr) && apiErr.Code == "invalid_phone_number" {
				message = "Telefon numarası geçersiz görünüyor. Lütfen ülke koduyla birlikte gerçek bir numara girin, örn. " + phoneExample + "."
			}
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(BookingResponse{Error: message})
			return
		}
		// No automatic retry: the order's true outcome at Duffel is unknown.
		// The lock stays at "unknown"; only a later resubmission's read-only
		// reconciliation lookup above may resolve it, never a fresh
		// CreateOrder call.
		price, _ := strconv.ParseFloat(offer.TotalAmount, 64)
		store.UpdateFlight(claimed.ID, user, "ordering", "unknown", nil)
		booking := &db.Booking{
			Type: "flight", FirstName: passengers[0].FirstName, LastName: passengers[0].LastName, Email: req.Email, Phone: phone,
			FromCode: req.FromCode, ToCode: req.ToCode, Airline: req.Airline,
			DepartTime: req.DepartTime, ArriveTime: req.ArriveTime, Duration: req.Duration, Stops: req.Stops,
			Price: price, Currency: offer.TotalCurrency, Status: "unknown",
		}
		applyOfferItinerary(booking, offer)
		ref, dbErr := h.DB.CreateBooking(booking)
		if dbErr != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(BookingResponse{Error: "booking failed, please try again"})
			return
		}
		json.NewEncoder(w).Encode(BookingResponse{Ref: ref, Status: "unknown"})
		return
	}

	// Duffel confirmed the order: record that fact durably before the local
	// write, so a failure in that write never risks a repeat CreateOrder.
	store.UpdateFlight(claimed.ID, user, "ordering", "order_placed", map[string]any{
		"order_id": order.ID, "booking_reference": order.BookingReference,
	})
	price, _ := strconv.ParseFloat(offer.TotalAmount, 64)
	resp, code := h.finalizeFlightOrder(offer, store, user, claimed, passengers[0], req, phone, offer.TotalCurrency, price, order.ID, order.BookingReference)
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}

// finalizeFlightOrder writes the local bookings-table row for an order
// already known to exist at Duffel (orderID/bookingReference may come from
// a CreateOrder call just made, or from an earlier attempt adopted through
// reconciliation) and moves the lock to "confirmed_sandbox". It never calls
// Duffel. If the local write fails, the lock is left at "order_placed" so a
// later request retries only this local step, with no further provider
// risk either way.
func (h *TravelHandler) finalizeFlightOrder(offer duffel.Offer, store db.FlightStore, user string, lock *db.FlightCheckout, lead FlightPassengerInput, req *BookingRequest, phone, currency string, price float64, orderID, bookingReference string) (BookingResponse, int) {
	booking := &db.Booking{
		Type: "flight", FirstName: lead.FirstName, LastName: lead.LastName, Email: req.Email, Phone: phone,
		FromCode: req.FromCode, ToCode: req.ToCode, Airline: req.Airline,
		DepartTime: req.DepartTime, ArriveTime: req.ArriveTime, Duration: req.Duration, Stops: req.Stops,
		Price: price, Currency: currency, Status: "confirmed_sandbox",
		DuffelOrderID: orderID, BookingReference: bookingReference,
	}
	applyOfferItinerary(booking, offer)
	ref, err := h.DB.CreateBooking(booking)
	if err != nil {
		return BookingResponse{Error: "booking failed, please try again"}, http.StatusInternalServerError
	}
	if current, won, lockErr := store.UpdateFlight(lock.ID, user, "order_placed", "confirmed_sandbox", map[string]any{"booking_ref": ref}); lockErr == nil && won {
		_ = current
		return BookingResponse{Ref: ref, Status: "confirmed_sandbox"}, http.StatusOK
	}
	// The CAS into confirmed_sandbox did not land (e.g. a concurrent
	// finalize already did, or the lock write itself failed). The Duffel
	// order and this local row both still exist; prefer a concurrent
	// winner's ref if there is one, to avoid handing out two refs for one
	// order.
	if current, _ := store.GetFlight(lock.ID, user); current != nil && current.Status == "confirmed_sandbox" && current.BookingRef != "" {
		return BookingResponse{Ref: current.BookingRef, Status: "confirmed_sandbox"}, http.StatusOK
	}
	return BookingResponse{Ref: ref, Status: "confirmed_sandbox"}, http.StatusOK
}

func (h *TravelHandler) MyBookingsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	email, err := auth.EmailFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	bookings, dbErr := h.DB.GetBookingsByEmail(email)
	if dbErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to fetch bookings"})
		return
	}
	if bookings == nil {
		bookings = []*db.Booking{}
	}
	if h.hotelClient().SandboxBookingEnabled() {
		user, err := auth.SubjectFromRequest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rows, err := h.hotelStore().ListHotels(user)
		if err != nil {
			http.Error(w, "hotel history unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, row := range rows {
			if row.Quote == nil {
				continue
			}
			q := row.Quote
			status := row.Status
			if status == "booking" {
				status = "unknown"
			}
			b := &db.Booking{Ref: row.ID, Type: "hotel", Email: email, HotelName: q.HotelName, Checkin: q.Checkin, Checkout: q.Checkout,
				Price: q.Price, Currency: q.Currency, Status: status, BookingReference: row.BookingID, CreatedAt: &row.CreatedAt}
			if row.Guest != nil {
				b.FirstName = row.Guest.FirstName
				b.LastName = row.Guest.LastName
			}
			bookings = append(bookings, b)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(bookings)
}

func (h *TravelHandler) CancelBookingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	email, err := auth.EmailFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		Ref string `json:"ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Ref == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "ref required"})
		return
	}
	if _, err := uuid.Parse(req.Ref); err == nil {
		// Hotel checkout rows are durable idempotency records. Removing them or
		// claiming a local deletion cancelled a provider booking is unsafe.
		hotelError(w, http.StatusConflict, "Hotel sandbox checkouts cannot be removed here. No provider cancellation was performed.")
		return
	}

	if err := h.DB.DeleteBooking(req.Ref, email); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "cancellation failed"})
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
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
	if len(d) < 4 {
		return ""
	}
	return d[len(d)-4:]
}

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"travel-proxy-service/internal/duffel"
	"travel-proxy-service/internal/liteapi"
	"travel-proxy-service/internal/proxy"
)

// cityCoords is a small fixed table; LiteAPI search is by coordinate only.
var cityCoords = map[string][2]float64{
	"paris":     {48.8566, 2.3522},
	"london":    {51.5074, -0.1278},
	"berlin":    {52.5200, 13.4050},
	"istanbul":  {41.0082, 28.9784},
	"antalya":   {36.8969, 30.7133},
	"rome":      {41.9028, 12.4964},
	"barcelona": {41.3851, 2.1734},
	"amsterdam": {52.3676, 4.9041},
	"tokyo":     {35.6762, 139.6503},
	"new york":  {40.7128, -74.0060},
	"dubai":     {25.2048, 55.2708},
	"prague":    {50.0755, 14.4378},
	"lisbon":    {38.7223, -9.1393},
}

const hotelSearchRadiusMeters = 10000 // 10km; confirmed empirically that radius is in meters

type searchLink struct{ Label, URL string }

func searchDate(raw string) (time.Time, error) {
	d, err := time.Parse("2006-01-02", raw)
	if err != nil || raw < time.Now().Format("2006-01-02") {
		return d, fmt.Errorf("Please choose a valid date today or in the future.")
	}
	return d, nil
}
func searchCount(raw string, fallback, min, max int) (string, error) {
	if raw == "" {
		raw = strconv.Itoa(fallback)
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return "", fmt.Errorf("Please check the number of travellers and rooms.")
	}
	return strconv.Itoa(n), nil
}
func externalGuests(q url.Values, hotel bool) (string, string, []string, error) {
	fallback := 1
	if hotel {
		fallback = 2
	}
	a, err := searchCount(q.Get("adults"), fallback, 1, 9)
	if err != nil {
		return "", "", nil, err
	}
	c, err := searchCount(q.Get("children"), 0, 0, 6)
	if err != nil {
		return "", "", nil, err
	}
	var ages []string
	if c != "0" {
		ages = strings.Split(q.Get("children_ages"), ",")
		n, _ := strconv.Atoi(c)
		if len(ages) != n {
			return "", "", nil, fmt.Errorf("Please select an age for each child.")
		}
		for _, age := range ages {
			if _, err := searchCount(age, -1, 0, 17); err != nil {
				return "", "", nil, fmt.Errorf("Please select an age for each child.")
			}
		}
	}
	return a, c, ages, nil
}

// hotelCategories maps a category to a title and cities within cityCoords
// only. "cabins" has no reasonable match among the 8 supported cities, so it
// is unsupported.
var hotelCategories = map[string]struct {
	title        string
	destinations []string
}{
	"city":       {"City Hotels", []string{"London", "Paris", "Berlin"}},
	"beach":      {"Beach Resorts", []string{"Antalya", "Barcelona"}},
	"apartments": {"Apartments", []string{"Amsterdam", "Barcelona", "Rome"}},
	"villas":     {"Private Villas", []string{"Antalya"}},
	"budget":     {"Hostels & Budget", []string{"Berlin", "Istanbul"}},
	"boutique":   {"Boutique Hotels", []string{"Paris", "Istanbul"}},
}

func (h *TravelHandler) externalHotels(w http.ResponseWriter, r *http.Request, pd pageData, category string) {
	q := r.URL.Query()
	pd.Nationality = strings.ToUpper(strings.TrimSpace(q.Get("guestNationality")))
	pd.HotelCurrency = strings.ToUpper(strings.TrimSpace(q.Get("currency")))
	if pd.HotelCurrency == "" {
		pd.HotelCurrency = "EUR"
	}
	fail := func(err error) { pd.Error = err.Error(); h.render(w, pd) }
	start, err := searchDate(pd.Checkin)
	if err != nil {
		fail(err)
		return
	}
	end, err := searchDate(pd.Checkout)
	if err != nil {
		fail(err)
		return
	}
	if !end.After(start) {
		fail(fmt.Errorf("Check-out must be after check-in."))
		return
	}
	a, children, ages, err := externalGuests(q, true)
	if err != nil {
		fail(err)
		return
	}
	rooms, err := searchCount(pd.Rooms, 1, 1, 9)
	if err != nil {
		fail(err)
		return
	}
	if rooms != "1" {
		fail(fmt.Errorf("Hotel search supports one room only; please search each room separately."))
		return
	}
	if children == "0" && q.Get("children_ages") != "" {
		fail(fmt.Errorf("Child ages must match the number of children."))
		return
	}
	childAges := make([]int, len(ages))
	for i, age := range ages {
		childAges[i], err = strconv.Atoi(age)
		if err != nil { // Empty ages must not silently become infants.
			fail(fmt.Errorf("Please select an age for each child."))
			return
		}
	}
	adults, _ := strconv.Atoi(a)
	query := liteapi.RateQuery{
		Occupancies: []liteapi.Occupancy{{Adults: adults, Children: childAges}},
		Checkin:     pd.Checkin, Checkout: pd.Checkout,
		Currency: pd.HotelCurrency, GuestNationality: pd.Nationality,
	}
	if err := query.Validate(); err != nil {
		fail(err)
		return
	}
	pd.Nights = int(end.Sub(start).Hours() / 24)
	if category == "" {
		destination, matched := proxy.ResolveHotelDestination(pd.City, q.Get("entityId"))
		if !matched {
			fail(fmt.Errorf("Bu şehir henüz desteklenmiyor."))
			return
		}
		pd.City, pd.CityEntityID = destination.Name, destination.EntityID
	}
	destinations := []string{strings.TrimSpace(pd.City)}
	if category != "" {
		cfg, ok := hotelCategories[category]
		if !ok {
			fail(fmt.Errorf("Bu kategori henüz desteklenmiyor."))
			return
		}
		destinations, pd.CategoryTitle = cfg.destinations, cfg.title
	}
	client := h.HotelClient
	if client == nil {
		client = liteapi.NewClient()
	}
	for _, city := range destinations {
		key := strings.ToLower(strings.TrimSpace(city))
		coords, ok := cityCoords[key]
		if !ok {
			fail(fmt.Errorf("Bu şehir henüz desteklenmiyor."))
			return
		}
		hotels, err := client.SearchHotels(coords[0], coords[1], hotelSearchRadiusMeters)
		if err != nil {
			if errors.Is(err, liteapi.ErrTimeout) {
				fail(fmt.Errorf("Arama zaman aşımına uğradı, tekrar deneyin."))
			} else {
				fail(fmt.Errorf("Otel araması tamamlanamadı, tekrar deneyin."))
			}
			return
		}
		if len(hotels) == 0 {
			continue
		}
		byID := make(map[string]liteapi.Hotel, len(hotels))
		ids := make([]string, 0, len(hotels))
		for _, hotel := range hotels {
			if hotel.ID == "" {
				continue
			}
			byID[hotel.ID] = hotel
			ids = append(ids, hotel.ID)
			if len(ids) >= 15 { // ~335KB/hotel observed live; keeps the response well under the size cap
				break
			}
		}
		rates, err := client.FetchRates(ids, query)
		if err != nil {
			fail(fmt.Errorf("Hotel prices could not be loaded. Please try again."))
			return
		}
		for _, hr := range rates {
			hotel, ok := byID[hr.HotelID]
			if !ok || len(hr.RoomTypes) == 0 {
				continue
			}
			cheapest, ok := hr.CheapestOffer()
			if !ok {
				continue
			}
			pd.Hotels = append(pd.Hotels, proxy.HotelData{
				HotelID: len(pd.Hotels) + 1, HotelName: hotel.Name, Destination: city,
				Price:      cheapest.OfferRetailRate.Amount / float64(pd.Nights),
				TotalPrice: cheapest.OfferRetailRate.Amount, Currency: cheapest.OfferRetailRate.Currency,
				Stars: hotel.Stars, Rating: hotel.Rating, PhotoURL: hotel.MainPhoto,
				ExternalHotelID: hotel.ID, RateID: cheapest.Rates[0].RateID, RoomOfferID: cheapest.OfferID,
			})
		}
	}
	if len(pd.Hotels) == 0 {
		fail(fmt.Errorf("Bu bölgede otel bulunamadı, farklı bir tarih veya şehir deneyin."))
		return
	}
	sort.SliceStable(pd.Hotels, func(i, j int) bool {
		if pd.Hotels[i].Currency != pd.Hotels[j].Currency {
			return pd.Hotels[i].Currency < pd.Hotels[j].Currency
		}
		return pd.Hotels[i].Price < pd.Hotels[j].Price
	})
	h.render(w, pd)
}
func (h *TravelHandler) externalFlights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pd := pageData{Tab: "flights", TripType: q.Get("tripType"), FromSkyID: strings.ToUpper(strings.TrimSpace(q.Get("fromSky"))), ToSkyID: strings.ToUpper(strings.TrimSpace(q.Get("toSky"))), Date: q.Get("date"), Adults: q.Get("adults"), Children: q.Get("children"), CabinClass: q.Get("cabinClass")}
	fail := func(message string) { pd.Error = message; h.render(w, pd) }
	if pd.TripType == "" {
		pd.TripType = "oneway"
	}
	if pd.CabinClass == "" {
		pd.CabinClass = "economy"
	}
	if pd.Adults == "" {
		pd.Adults = "1"
	}
	if pd.Children == "" {
		pd.Children = "0"
	}
	switch pd.TripType {
	case "oneway":
	case "round":
		pd.ReturnDate = strings.TrimSpace(q.Get("returnDate"))
	case "multi":
	default:
		fail("Please choose a valid trip type.")
		return
	}
	adults, children, ages, err := externalGuests(q, false)
	if err != nil {
		fail(err.Error())
		return
	}
	if children == "0" && q.Get("children_ages") != "" {
		fail("Child ages must match the number of children.")
		return
	}
	pd.Adults, pd.Children = adults, children
	adultCount, _ := strconv.Atoi(adults)
	childCount, _ := strconv.Atoi(children)
	if adultCount+childCount > 9 {
		fail("Please select no more than 9 passengers.")
		return
	}
	passengers := make([]duffel.SearchPassenger, 0, adultCount+childCount)
	for i := 0; i < adultCount; i++ {
		passengers = append(passengers, duffel.SearchPassenger{Type: "adult"})
	}
	for _, raw := range ages {
		age, _ := strconv.Atoi(raw)
		passengers = append(passengers, duffel.SearchPassenger{Age: &age})
	}
	priceLabel := fmt.Sprintf("Total for %s adult", adults)
	if adultCount > 1 {
		priceLabel += "s"
	}
	if childCount > 0 {
		if childCount == 1 {
			priceLabel += " · 1 child"
		} else {
			priceLabel += fmt.Sprintf(" · %s children", children)
		}
	}
	switch pd.CabinClass {
	case "economy", "premium_economy", "business", "first":
	default:
		fail("Please choose a valid cabin class.")
		return
	}
	slices, ok := h.flightSearchSlices(w, q, &pd, fail)
	if !ok {
		return
	}
	if len(slices) > 1 {
		// One offer prices the whole itinerary, all flights included.
		priceLabel = "Itinerary total" + strings.TrimPrefix(priceLabel, "Total")
	}
	offers, err := duffel.NewDuffelClient().CreateOfferRequest(
		slices,
		passengers,
		pd.CabinClass,
	)
	if err != nil {
		if errors.Is(err, duffel.ErrTimeout) {
			fail("Arama zaman aşımına uğradı, tekrar deneyin")
		} else {
			fail("Uçuş araması tamamlanamadı, tekrar deneyin")
		}
		return
	}
	if len(offers) == 0 {
		fail("Uçuş bulunamadı")
		return
	}
	for _, offer := range offers {
		// An offer must cover exactly the requested slices; anything else
		// would show a price for a different itinerary than the one searched.
		if offer.ID == "" || len(offer.Slices) != len(slices) {
			continue
		}
		price, err := strconv.ParseFloat(offer.TotalAmount, 64) // Display only; never use for an order payment.
		if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			continue
		}
		legs, ok := offerItineraryLegs(offer)
		if !ok {
			continue
		}
		first, last := legs[0], legs[len(legs)-1]
		totalMinutes, totalStops := 0, 0
		for _, leg := range legs {
			totalMinutes += leg.DurationHours*60 + leg.DurationMinutes
			totalStops += leg.Stops
		}
		legsJSON, err := json.Marshal(legs)
		if err != nil {
			continue
		}
		pd.Flights = append(pd.Flights, proxy.FlightData{
			OfferID:  offer.ID,
			FromCode: first.FromCode, ToCode: last.ToCode,
			FromCity: first.FromCity, ToCity: last.ToCity,
			DepartDate: first.DepartTime[:10],
			DepartTime: first.DepartTime, ArriveTime: last.ArriveTime,
			DurationHours: totalMinutes / 60, DurationMinutes: totalMinutes % 60,
			Stops:   totalStops,
			Airline: first.Airline, AirlineLogo: first.AirlineLogo,
			Price: price, Currency: offer.TotalCurrency, PriceLabel: priceLabel,
			AdultCount: adultCount, ChildAgesCSV: strings.Join(ages, ","),
			Legs: legs, RouteLabel: routeLabel(legs), LegsJSON: string(legsJSON),
		})
	}
	if len(pd.Flights) == 0 {
		fail("Uçuş sonuçları okunamadı, tekrar deneyin")
		return
	}
	sort.SliceStable(pd.Flights, func(i, j int) bool { return pd.Flights[i].Price < pd.Flights[j].Price })
	if len(pd.Flights) > 20 {
		pd.Flights = pd.Flights[:20]
	}
	pd.FromCity, pd.ToCity = pd.Flights[0].Legs[0].FromCity, pd.Flights[0].Legs[0].ToCity
	h.render(w, pd)
}

// maxFlightSlices matches the six legs the multi-city form allows.
const maxFlightSlices = 6

func validAirportCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, letter := range code {
		if letter < 'A' || letter > 'Z' {
			return false
		}
	}
	return true
}

// flightSearchSlices builds the Duffel slices for the requested trip type:
// one for one-way, two (outbound and return) for a round trip, and 2-6 for
// multi-city. It reports false when it has already rendered the page, either
// because no search was requested yet or because the input was invalid.
func (h *TravelHandler) flightSearchSlices(w http.ResponseWriter, q url.Values, pd *pageData, fail func(string)) ([]duffel.SearchSlice, bool) {
	if pd.TripType == "multi" {
		for i := 0; i < maxFlightSlices; i++ {
			leg := proxy.FlightSearchLeg{
				From: strings.ToUpper(strings.TrimSpace(q.Get(fmt.Sprintf("leg%dfrom", i)))),
				To:   strings.ToUpper(strings.TrimSpace(q.Get(fmt.Sprintf("leg%dto", i)))),
				Date: strings.TrimSpace(q.Get(fmt.Sprintf("leg%ddate", i))),
			}
			if leg.From == "" && leg.To == "" && leg.Date == "" {
				continue
			}
			pd.SearchLegs = append(pd.SearchLegs, leg)
		}
		if len(pd.SearchLegs) == 0 {
			h.render(w, *pd)
			return nil, false
		}
		if len(pd.SearchLegs) < 2 {
			fail("Add at least two flights for a multi-city trip.")
			return nil, false
		}
		slices := make([]duffel.SearchSlice, 0, len(pd.SearchLegs))
		for i, leg := range pd.SearchLegs {
			if !validAirportCode(leg.From) || !validAirportCode(leg.To) || leg.From == leg.To {
				fail(fmt.Sprintf("Flight %d: enter valid three-letter airport codes, such as BER and LHR.", i+1))
				return nil, false
			}
			if _, err := searchDate(leg.Date); err != nil {
				fail(fmt.Sprintf("Flight %d: %s", i+1, err.Error()))
				return nil, false
			}
			if i > 0 && leg.Date < pd.SearchLegs[i-1].Date {
				fail("Flight dates must be in chronological order.")
				return nil, false
			}
			slices = append(slices, duffel.SearchSlice{Origin: leg.From, Destination: leg.To, DepartureDate: leg.Date})
		}
		return slices, true
	}
	if pd.FromSkyID == "" && pd.ToSkyID == "" {
		h.render(w, *pd)
		return nil, false
	}
	if !validAirportCode(pd.FromSkyID) || !validAirportCode(pd.ToSkyID) || pd.FromSkyID == pd.ToSkyID {
		fail("Enter valid three-letter airport codes, such as BER and LHR.")
		return nil, false
	}
	if pd.Date == "" {
		pd.Date = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	}
	if _, err := searchDate(pd.Date); err != nil {
		fail(err.Error())
		return nil, false
	}
	slices := []duffel.SearchSlice{{Origin: pd.FromSkyID, Destination: pd.ToSkyID, DepartureDate: pd.Date}}
	if pd.TripType == "round" {
		if pd.ReturnDate == "" {
			fail("Please choose a return date.")
			return nil, false
		}
		if _, err := searchDate(pd.ReturnDate); err != nil {
			fail(err.Error())
			return nil, false
		}
		if pd.ReturnDate < pd.Date {
			fail("The return date must be on or after the departure date.")
			return nil, false
		}
		slices = append(slices, duffel.SearchSlice{Origin: pd.ToSkyID, Destination: pd.FromSkyID, DepartureDate: pd.ReturnDate})
	}
	return slices, true
}

// offerItineraryLegs summarises every slice of an offer, in order. It
// reports false when any slice lacks the data needed to display it
// truthfully, so a partial itinerary is never shown or booked.
func offerItineraryLegs(offer duffel.Offer) ([]proxy.FlightItineraryLeg, bool) {
	if len(offer.Slices) == 0 {
		return nil, false
	}
	legs := make([]proxy.FlightItineraryLeg, 0, len(offer.Slices))
	for _, slice := range offer.Slices {
		if len(slice.Segments) == 0 {
			return nil, false
		}
		hours, minutes, err := parseFlightDuration(slice.Duration)
		if err != nil {
			return nil, false
		}
		first, last := slice.Segments[0], slice.Segments[len(slice.Segments)-1]
		if len(first.DepartingAt) < 16 || len(last.ArrivingAt) < 16 ||
			first.Origin.IATACode == "" || last.Destination.IATACode == "" {
			return nil, false
		}
		legs = append(legs, proxy.FlightItineraryLeg{
			FromCode: first.Origin.IATACode, ToCode: last.Destination.IATACode,
			FromCity: first.Origin.CityName, ToCity: last.Destination.CityName,
			DepartTime: first.DepartingAt, ArriveTime: last.ArrivingAt,
			DurationHours: hours, DurationMinutes: minutes,
			Airline: first.MarketingCarrier.Name, AirlineLogo: first.MarketingCarrier.LogoSymbolURL,
			Stops: len(slice.Segments) - 1,
		})
	}
	return legs, true
}

// routeLabel renders "BER → LHR → BER" for connected legs and separates
// disconnected legs with " · ", e.g. "BER → LHR · CDG → BER".
func routeLabel(legs []proxy.FlightItineraryLeg) string {
	var b strings.Builder
	for i, leg := range legs {
		switch {
		case i == 0:
			b.WriteString(leg.FromCode)
		case leg.FromCode != legs[i-1].ToCode:
			b.WriteString(" · " + leg.FromCode)
		}
		b.WriteString(" → " + leg.ToCode)
	}
	return b.String()
}

// Parse the PT...H...M subset used by this search; reject unsupported formats.
var flightDurationPattern = regexp.MustCompile(`^PT(?:[0-9]+H)?(?:[0-9]+M)?$`)

func parseFlightDuration(raw string) (int, int, error) {
	if raw == "PT" || !flightDurationPattern.MatchString(raw) {
		return 0, 0, fmt.Errorf("unsupported flight duration")
	}
	duration, err := time.ParseDuration(strings.ToLower(strings.TrimPrefix(raw, "PT")))
	if err != nil {
		return 0, 0, err
	}
	return int(duration / time.Hour), int(duration/time.Minute) % 60, nil
}

package handlers

import (
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
	if pd.TripType != "oneway" || q.Get("returnDate") != "" {
		fail("Şu anda yalnızca tek yön uçuş araması destekleniyor.")
		return
	}
	adults, err := searchCount(pd.Adults, 1, 1, 1)
	if err != nil {
		fail("Şu anda yalnızca tek yetişkin yolcu destekleniyor.")
		return
	}
	children, err := searchCount(pd.Children, 0, 0, 0)
	if err != nil || q.Get("children_ages") != "" {
		fail("Şu anda yalnızca tek yetişkin yolcu destekleniyor.")
		return
	}
	pd.Adults, pd.Children = adults, children
	switch pd.CabinClass {
	case "economy", "premium_economy", "business", "first":
	default:
		fail("Please choose a valid cabin class.")
		return
	}
	if pd.FromSkyID == "" && pd.ToSkyID == "" {
		h.render(w, pd)
		return
	}
	validCode := func(code string) bool {
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
	if !validCode(pd.FromSkyID) || !validCode(pd.ToSkyID) || pd.FromSkyID == pd.ToSkyID {
		fail("Enter valid three-letter airport codes, such as BER and LHR.")
		return
	}
	if pd.Date == "" {
		pd.Date = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	}
	if _, err := searchDate(pd.Date); err != nil {
		fail(err.Error())
		return
	}
	offers, err := duffel.NewDuffelClient().CreateOfferRequest(
		[]duffel.SearchSlice{{Origin: pd.FromSkyID, Destination: pd.ToSkyID, DepartureDate: pd.Date}},
		[]duffel.SearchPassenger{{Type: "adult"}},
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
		if offer.ID == "" || len(offer.Slices) != 1 || len(offer.Slices[0].Segments) == 0 {
			continue
		}
		price, err := strconv.ParseFloat(offer.TotalAmount, 64) // Display only; never use for an order payment.
		if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			continue
		}
		slice := offer.Slices[0]
		hours, minutes, err := parseFlightDuration(slice.Duration)
		if err != nil {
			continue
		}
		first, last := slice.Segments[0], slice.Segments[len(slice.Segments)-1]
		if len(first.DepartingAt) < 16 || len(last.ArrivingAt) < 16 {
			continue
		}
		pd.Flights = append(pd.Flights, proxy.FlightData{
			OfferID:  offer.ID,
			FromCode: first.Origin.IATACode, ToCode: last.Destination.IATACode,
			FromCity: first.Origin.CityName, ToCity: last.Destination.CityName,
			DepartDate: first.DepartingAt[:10],
			DepartTime: first.DepartingAt, ArriveTime: last.ArrivingAt,
			DurationHours: hours, DurationMinutes: minutes,
			Stops:   len(slice.Segments) - 1,
			Airline: first.MarketingCarrier.Name, AirlineLogo: first.MarketingCarrier.LogoSymbolURL,
			Price: price, Currency: offer.TotalCurrency, PriceLabel: "1 adult",
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
	pd.FromCity, pd.ToCity = pd.Flights[0].FromCity, pd.Flights[0].ToCity
	h.render(w, pd)
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

package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	rapidAPIKey = "8c145b23a1msh5288f08d5058e73p18692ejsn1e8fb15260c9"

	skyHost     = "skyscanner-flights-travel-api.p.rapidapi.com"
	skyBase     = "https://skyscanner-flights-travel-api.p.rapidapi.com"

	gfHost      = "google-flights2.p.rapidapi.com"
	gfBase      = "https://google-flights2.p.rapidapi.com"

	hotelsHost  = "hotels-com-provider.p.rapidapi.com"
	hotelsBase  = "https://hotels-com-provider.p.rapidapi.com"

	bookingHost = "booking-com.p.rapidapi.com"
	bookingBase = "https://booking-com.p.rapidapi.com"
)

// CarData is the flat struct for car rental listings.
type CarData struct {
	CarID       int     `json:"car_id"`
	CarName     string  `json:"car_name"`
	Category    string  `json:"category"`
	Seats       int     `json:"seats"`
	Doors       int     `json:"doors"`
	Transmission string `json:"transmission"`
	PricePerDay float64 `json:"price_per_day"`
	OriginalPrice float64 `json:"original_price"`
	Currency    string  `json:"currency"`
	Provider    string  `json:"provider"`
	ProviderLogo string `json:"provider_logo"`
	Badge       string  `json:"badge"`
	CategoryColor string `json:"category_color"`
}

// HotelData is the flat struct passed to templates and JSON API.
type HotelData struct {
	HotelID    int     `json:"hotel_id"`
	HotelName  string  `json:"hotel_name"`
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
	Rating     float64 `json:"rating"`
	RatingWord string  `json:"rating_word"`
	PhotoURL   string  `json:"photo_url"`
	Stars      int     `json:"stars"`
}

// FlightData is the flat struct for flight offers.
type FlightData struct {
	FromCity        string  `json:"from_city"`
	ToCity          string  `json:"to_city"`
	FromCode        string  `json:"from_code"`
	ToCode          string  `json:"to_code"`
	DepartTime      string  `json:"depart_time"`
	ArriveTime      string  `json:"arrive_time"`
	DurationHours   int     `json:"duration_hours"`
	DurationMinutes int     `json:"duration_minutes"`
	Airline         string  `json:"airline"`
	AirlineLogo     string  `json:"airline_logo"`
	Price           float64 `json:"price"`
	Currency        string  `json:"currency"`
	Stops           int     `json:"stops"`
}

// DestSuggestion is returned by the /suggest endpoint (hotel city search).
type DestSuggestion struct {
	EntityID  string `json:"entityId"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Hierarchy string `json:"hierarchy"`
}

// FlightDestSuggestion is returned by the /suggest-flight endpoint.
type FlightDestSuggestion struct {
	SkyID       string `json:"skyId"`
	EntityID    string `json:"entityId"`
	Name        string `json:"name"`
	CityName    string `json:"cityName"`
	CountryName string `json:"countryName"`
	PlaceType   string `json:"placeType"`
}

// --- Google Flights internal structs ---

type gfAirportEntry struct {
	ID   string           `json:"id"`
	Type string           `json:"type"`
	Name string           `json:"title"`
	City string           `json:"city"`
	List []gfAirportEntry `json:"list"`
}

type gfAirportResponse struct {
	Data []gfAirportEntry `json:"data"`
}

type gfAirport struct {
	AirportCode string `json:"airport_code"`
	AirportName string `json:"airport_name"`
}

type gfFlightSegment struct {
	DepartureAirport gfAirport `json:"departure_airport"`
	ArrivalAirport   gfAirport `json:"arrival_airport"`
	Airline          string    `json:"airline"`
	AirlineLogo      string    `json:"airline_logo"`
}

type gfDuration struct {
	Raw int `json:"raw"`
}

type gfLayover struct {
	AirportCode string `json:"airport_code"`
}

type gfItinerary struct {
	DepartureTime string            `json:"departure_time"`
	ArrivalTime   string            `json:"arrival_time"`
	Duration      gfDuration        `json:"duration"`
	Flights       []gfFlightSegment `json:"flights"`
	Layovers      []gfLayover       `json:"layovers"`
	AirlineLogo   string            `json:"airline_logo"`
	Price         float64           `json:"price"`
}

type gfItineraries struct {
	TopFlights   []gfItinerary `json:"topFlights"`
	OtherFlights []gfItinerary `json:"otherFlights"`
}

type gfFlightData struct {
	Itineraries gfItineraries `json:"itineraries"`
}

type gfFlightResponse struct {
	Status bool         `json:"status"`
	Data   gfFlightData `json:"data"`
}

// --- Skyscanner internal structs ---

type skyAirportEntry struct {
	EntityID   string `json:"entityId"`
	EntityType string `json:"entityType"`
	Name       string `json:"name"`
	IataCode   string `json:"iataCode"`
	Location   string `json:"location"`
}

type skyAirportResponse struct {
	Status bool              `json:"status"`
	Data   []skyAirportEntry `json:"data"`
}

type skyPrice struct {
	Raw       float64 `json:"raw"`
	Formatted string  `json:"formatted"`
}

type skyPlace struct {
	ID          string `json:"id"`
	City        string `json:"city"`
	Country     string `json:"country"`
	DisplayCode string `json:"displayCode"`
}

type skyCarrierInfo struct {
	ID      int    `json:"id"`
	LogoURL string `json:"logoUrl"`
	Name    string `json:"name"`
}

type skyLeg struct {
	ID                string   `json:"id"`
	Origin            skyPlace `json:"origin"`
	Destination       skyPlace `json:"destination"`
	DurationInMinutes int      `json:"durationInMinutes"`
	StopCount         int      `json:"stopCount"`
	Departure         string   `json:"departure"`
	Arrival           string   `json:"arrival"`
	Carriers          struct {
		Marketing []skyCarrierInfo `json:"marketing"`
	} `json:"carriers"`
}

type skyItinerary struct {
	ID    string   `json:"id"`
	Price skyPrice `json:"price"`
	Legs  []skyLeg `json:"legs"`
}

type skyFlightData struct {
	Itineraries []skyItinerary `json:"itineraries"`
}

type skyFlightResponse struct {
	Status bool          `json:"status"`
	Data   skyFlightData `json:"data"`
}

// --- Hotels.com regions internal structs ---

type hcRegion struct {
	GaiaID      string `json:"gaiaId"`
	Type        string `json:"type"`
	RegionNames struct {
		FullName    string `json:"fullName"`
		ShortName   string `json:"shortName"`
		DisplayName string `json:"displayName"`
	} `json:"regionNames"`
	HierarchyInfo struct {
		Country struct {
			Name string `json:"name"`
		} `json:"country"`
	} `json:"hierarchyInfo"`
}

type hcRegionResponse struct {
	Data []hcRegion `json:"data"`
}

type bookingDestResponse struct {
	Result []struct {
		DestID   string `json:"dest_id"`
		DestType string `json:"dest_type"`
		Label    string `json:"label"`
	} `json:"result"`
}

type bookingHotelSearchResponse struct {
	Result []struct {
		HotelID        int     `json:"hotel_id"`
		HotelName      string  `json:"hotel_name"`
		Class          float64 `json:"class"`
		ReviewScore    float64 `json:"review_score"`
		ReviewWordEN   string  `json:"review_score_word"`
		MinTotalPrice  float64 `json:"min_total_price"`
		CurrencyCode   string  `json:"currencycode"`
		MaxPhotoURL    string  `json:"max_photo_url"`
		MainPhotoURL   string  `json:"main_photo_url"`
	} `json:"result"`
}

// approximate conversion rates to USD (good enough for display)
var toUSD = map[string]float64{
	"JPY": 1.0 / 150.0,
	"TRY": 1.0 / 32.0,
	"EUR": 1.0 / 0.92,
	"GBP": 1.0 / 0.79,
	"AED": 1.0 / 3.67,
	"THB": 1.0 / 35.0,
	"SGD": 1.0 / 1.35,
	"AUD": 1.0 / 1.52,
	"CAD": 1.0 / 1.36,
	"CHF": 1.0 / 0.90,
	"INR": 1.0 / 83.0,
	"MXN": 1.0 / 17.5,
	"BRL": 1.0 / 5.0,
}

type hcHotelSearchResponse struct {
	Data struct {
		Body struct {
			SearchResults struct {
				Results []struct {
					ID         int    `json:"id"`
					Name       string `json:"name"`
					StarRating int    `json:"starRating"`
					GuestReviews struct {
						UnformattedRating float64 `json:"unformattedRating"`
						Brands            string  `json:"brands"`
					} `json:"guestReviews"`
					RatePlan struct {
						Price struct {
							ExactCurrent float64 `json:"exactCurrent"`
						} `json:"price"`
					} `json:"ratePlan"`
					OptimizedThumbUrls struct {
						SrpDesktop string `json:"srpDesktop"`
					} `json:"optimizedThumbUrls"`
					ThumbnailURL string `json:"thumbnailUrl"`
				} `json:"results"`
			} `json:"searchResults"`
		} `json:"body"`
	} `json:"data"`
}

// ---

type ProxyClient struct {
	HTTPClient *http.Client
	Amadeus    *AmadeusClient
}

func NewProxyClient(_ string) *ProxyClient {
	return &ProxyClient{
		HTTPClient: &http.Client{Timeout: 12 * time.Second},
		Amadeus:    NewAmadeusClient(os.Getenv("AMADEUS_CLIENT_ID"), os.Getenv("AMADEUS_CLIENT_SECRET")),
	}
}

func (pc *ProxyClient) doGet(base, host, endpoint string, params map[string]string) (*http.Response, error) {
	req, err := http.NewRequest("GET", base+endpoint, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("X-RapidAPI-Key", rapidAPIKey)
	req.Header.Set("X-RapidAPI-Host", host)
	return pc.HTTPClient.Do(req)
}

// SearchHotelDestinations returns city suggestions for hotel autocomplete via Booking.com API.
func (pc *ProxyClient) SearchHotelDestinations(query string) ([]DestSuggestion, error) {
	resp, err := pc.doGet(bookingBase, bookingHost, "/v1/hotels/locations", map[string]string{
		"name":   query,
		"locale": "en-us",
	})
	if err != nil {
		return nil, fmt.Errorf("hotel destination search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload []struct {
		DestID   string `json:"dest_id"`
		DestType string `json:"dest_type"`
		Label    string `json:"label"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode hotel destination response: %w", err)
	}

	results := make([]DestSuggestion, 0)
	for _, r := range payload {
		if r.DestType != "city" && r.DestType != "region" {
			continue
		}
		results = append(results, DestSuggestion{
			EntityID:  r.DestID,
			Name:      r.Label,
			Type:      r.DestType,
			Hierarchy: "",
		})
		if len(results) >= 6 {
			break
		}
	}
	return results, nil
}

// SearchHotelDestination returns the first city dest_id for a query.
func (pc *ProxyClient) SearchHotelDestination(query string) (string, error) {
	results, err := pc.SearchHotelDestinations(query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", fmt.Errorf("no destination found for %q", query)
	}
	return results[0].EntityID, nil
}

// SearchAirports returns airport suggestions using Google Flights airport search.
func (pc *ProxyClient) SearchAirports(query string) ([]FlightDestSuggestion, error) {
	resp, err := pc.doGet(gfBase, gfHost, "/api/v1/searchAirport", map[string]string{"query": query})
	if err != nil {
		return nil, fmt.Errorf("airport search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload gfAirportResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode airport response: %w", err)
	}

	var results []FlightDestSuggestion
	addResult := func(id, name, city, country string) {
		if len(id) != 3 {
			return
		}
		for _, r := range results {
			if r.SkyID == id {
				return // deduplicate
			}
		}
		results = append(results, FlightDestSuggestion{
			SkyID:       id,
			EntityID:    id,
			Name:        name,
			CityName:    city,
			CountryName: country,
			PlaceType:   "airport",
		})
	}
	for _, group := range payload.Data {
		country := ""
		if idx := strings.LastIndex(group.Name, ", "); idx >= 0 {
			country = group.Name[idx+2:]
		}
		// group itself may be an airport entry (e.g. direct IATA code search)
		addResult(group.ID, group.Name, group.City, country)
		// nested list items
		for _, a := range group.List {
			addResult(a.ID, a.Name, a.City, country)
		}
		if len(results) >= 8 {
			break
		}
	}
	return results, nil
}

// resolveSkyscannerEntityID resolves an IATA code to a Skyscanner entity ID via autocomplete.
func (pc *ProxyClient) resolveSkyscannerEntityID(iata string) string {
	resp, err := pc.doGet(skyBase, skyHost, "/flights/auto-complete", map[string]string{"query": iata})
	if err != nil {
		log.Printf("[SKY-AC] iata=%s http_err=%v", iata, err)
		return iata
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.Printf("[SKY-AC] iata=%s status=%d body_prefix=%.200s", iata, resp.StatusCode, string(body))
	var payload skyAirportResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return iata
	}
	for _, a := range payload.Data {
		if a.IataCode == iata {
			return a.EntityID
		}
	}
	if len(payload.Data) > 0 && payload.Data[0].EntityID != "" {
		return payload.Data[0].EntityID
	}
	return iata
}

// hotelTemplates define names, stars and pricing only; photos are generated dynamically per city.
var hotelTemplates = []struct {
	suffix string
	stars  int
	base   float64
	rating float64
	word   string
}{
	{"Grand Palace Hotel", 5, 220, 9.2, "Exceptional"},
	{"Boutique Centrale", 4, 135, 8.8, "Excellent"},
	{"The Riverside Suites", 4, 160, 8.5, "Excellent"},
	{"Luxury Tower & Spa", 5, 310, 9.5, "Exceptional"},
	{"City View Residences", 3, 88, 7.9, "Good"},
	{"Heritage Collection", 4, 175, 9.0, "Exceptional"},
	{"The Modern Stay", 4, 145, 8.6, "Excellent"},
	{"Skyline Boutique", 3, 99, 8.1, "Very Good"},
	{"Art Deco Suites", 4, 195, 8.9, "Excellent"},
	{"Garden Quarter Inn", 3, 75, 7.7, "Good"},
	{"Panorama Rooftop Hotel", 5, 280, 9.3, "Exceptional"},
	{"The Old Town Lodge", 3, 65, 8.0, "Very Good"},
}

// fetchMockHotels generates deterministic mock listings seeded by city name.
func (pc *ProxyClient) fetchMockHotels(city string) ([]HotelData, error) {
	seed := int64(0)
	for _, c := range city {
		seed = seed*31 + int64(c)
	}
	if seed < 0 {
		seed = -seed
	}
	hotels := make([]HotelData, 0, len(hotelTemplates))
	for i := range hotelTemplates {
		idx := (int(seed) + i) % len(hotelTemplates)
		tpl := hotelTemplates[idx]
		name := city + " " + tpl.suffix
		price := tpl.base + float64((int(seed)+i)%5)*11.0
		picID := 200 + (int(seed)+i*7)%800
		photo := fmt.Sprintf("https://picsum.photos/id/%d/600/400", picID)
		hotels = append(hotels, HotelData{
			HotelID:    int(seed%9000) + 1000 + i,
			HotelName:  name,
			Price:      price,
			Currency:   "USD",
			Rating:     tpl.rating,
			RatingWord: tpl.word,
			PhotoURL:   photo,
			Stars:      tpl.stars,
		})
	}
	return hotels, nil
}

var carTemplates = []struct {
	name         string
	category     string
	categoryColor string
	seats        int
	doors        int
	transmission string
	basePrice    float64
	provider     string
	providerLogo string
	badge        string
}{
	{"Toyota Yaris", "Economy", "#4f46e5", 5, 4, "Automatic", 29, "Hertz", "hertz.com", "Free cancel"},
	{"Volkswagen Golf", "Compact", "#0284c7", 5, 4, "Automatic", 38, "Avis", "avis.com", "Free cancel"},
	{"Toyota RAV4", "SUV", "#b45309", 7, 5, "Automatic", 62, "Sixt", "sixt.com", "Unlimited km"},
	{"Mercedes-Benz C-Class", "Luxury", "#7c3aed", 5, 4, "Automatic", 145, "Europcar", "europcar.com", "Free cancel"},
	{"Fiat 500", "Economy", "#4f46e5", 4, 3, "Manual", 24, "Budget", "budget.com", "-20% today"},
	{"Ford Mustang", "Sports", "#dc2626", 4, 2, "Automatic", 110, "Hertz", "hertz.com", "Hot deal"},
	{"BMW X5", "SUV", "#b45309", 7, 5, "Automatic", 185, "Sixt", "sixt.com", "Premium"},
	{"Renault Clio", "Economy", "#4f46e5", 5, 4, "Manual", 22, "Europcar", "europcar.com", "Free cancel"},
	{"Audi A4", "Compact", "#0284c7", 5, 4, "Automatic", 75, "Avis", "avis.com", "Free cancel"},
}

// cityPriceMultiplier returns a cost-of-living multiplier for a city.
func cityPriceMultiplier(city string) float64 {
	expensive := map[string]float64{
		"dubai": 1.9, "singapore": 1.8, "new york": 1.7, "london": 1.6,
		"zurich": 1.8, "geneva": 1.75, "oslo": 1.65, "stockholm": 1.5,
		"paris": 1.45, "amsterdam": 1.4, "copenhagen": 1.55, "tokyo": 1.35,
		"sydney": 1.4, "melbourne": 1.35, "toronto": 1.3, "vancouver": 1.3,
		"barcelona": 1.1, "rome": 1.05, "madrid": 1.05, "berlin": 1.1,
		"munich": 1.2, "vienna": 1.15, "prague": 0.75, "budapest": 0.7,
		"warsaw": 0.72, "bucharest": 0.65, "sofia": 0.6, "athens": 0.85,
		"istanbul": 0.8, "cairo": 0.55, "bangkok": 0.6, "bali": 0.55,
		"lisbon": 0.9, "porto": 0.85, "miami": 1.25, "los angeles": 1.3,
		"chicago": 1.2, "dallas": 1.1, "mexico city": 0.7, "bogota": 0.65,
	}
	lower := strings.ToLower(city)
	if m, ok := expensive[lower]; ok {
		return m
	}
	// deterministic fallback for unknown cities
	h := int64(0)
	for _, c := range lower {
		h = h*31 + int64(c)
	}
	if h < 0 {
		h = -h
	}
	return 0.7 + float64(h%60)/100.0 // 0.70 - 1.30
}

// FetchCarsByCity returns deterministic mock car listings seeded by city.
func (pc *ProxyClient) FetchCarsByCity(city string) []CarData {
	seed := int64(0)
	for _, c := range city {
		seed = seed*31 + int64(c)
	}
	if seed < 0 {
		seed = -seed
	}
	mult := cityPriceMultiplier(city)
	cars := make([]CarData, 0, len(carTemplates))
	for i := range carTemplates {
		idx := (int(seed) + i*7) % len(carTemplates)
		t := carTemplates[idx]
		// per-car variation ±15% based on seed+position
		varPct := 1.0 + float64(((int(seed)+i*13)%31)-15)/100.0
		price := math.Round(t.basePrice*mult*varPct)
		if price < 10 {
			price = 10
		}
		orig := 0.0
		if t.badge == "-20% today" {
			orig = math.Round(price / 0.8)
		}
		cars = append(cars, CarData{
			CarID:         int(seed%9000) + 1000 + i,
			CarName:       t.name,
			Category:      t.category,
			CategoryColor: t.categoryColor,
			Seats:         t.seats,
			Doors:         t.doors,
			Transmission:  t.transmission,
			PricePerDay:   price,
			OriginalPrice: orig,
			Currency:      "USD",
			Provider:      t.provider,
			ProviderLogo:  "https://logo.clearbit.com/" + t.providerLogo,
			Badge:         t.badge,
		})
	}
	return cars
}

// fetchHotelsFromBooking fetches real hotel listings with photos via Booking.com RapidAPI.
func (pc *ProxyClient) fetchHotelsFromBooking(city, checkIn, checkOut, adults, rooms string) ([]HotelData, error) {
	// Step 1: resolve dest_id (response is a direct array, not wrapped)
	destResp, err := pc.doGet(bookingBase, bookingHost, "/v1/hotels/locations", map[string]string{
		"name":   city,
		"locale": "en-us",
	})
	if err != nil {
		return nil, fmt.Errorf("booking dest search: %w", err)
	}
	defer destResp.Body.Close()
	var destList []struct {
		DestID   string `json:"dest_id"`
		DestType string `json:"dest_type"`
		Label    string `json:"label"`
	}
	if err := json.NewDecoder(destResp.Body).Decode(&destList); err != nil {
		return nil, fmt.Errorf("booking dest decode: %w", err)
	}
	if len(destList) == 0 {
		return nil, fmt.Errorf("booking: no destination for %s", city)
	}
	dest := destList[0]

	// Step 2: search hotels
	resp, err := pc.doGet(bookingBase, bookingHost, "/v1/hotels/search", map[string]string{
		"dest_id":           dest.DestID,
		"dest_type":         dest.DestType,
		"checkin_date":      checkIn,
		"checkout_date":     checkOut,
		"adults_number":     adults,
		"room_number":       rooms,
		"locale":            "en-us",
		"currency":          "USD",
		"order_by":          "popularity",
		"units":             "metric",
		"filter_by_currency": "USD",
	})
	if err != nil {
		return nil, fmt.Errorf("booking hotel search: %w", err)
	}
	defer resp.Body.Close()
	var payload bookingHotelSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("booking hotel decode: %w", err)
	}
	if len(payload.Result) == 0 {
		return nil, fmt.Errorf("booking returned no hotels")
	}

	// calculate number of nights for per-night price
	nights := 1.0
	if t1, err1 := time.Parse("2006-01-02", checkIn); err1 == nil {
		if t2, err2 := time.Parse("2006-01-02", checkOut); err2 == nil {
			if d := t2.Sub(t1).Hours() / 24; d > 0 {
				nights = d
			}
		}
	}

	hotels := make([]HotelData, 0, len(payload.Result))
	for i, r := range payload.Result {
		photo := r.MaxPhotoURL
		if photo == "" {
			photo = r.MainPhotoURL
		}
		stars := int(r.Class)
		if stars == 0 {
			stars = 3
		}
		pricePerNight := r.MinTotalPrice / nights
		// convert to USD if API returned local currency
		if r.CurrencyCode != "" && r.CurrencyCode != "USD" {
			if rate, ok := toUSD[r.CurrencyCode]; ok {
				pricePerNight = pricePerNight * rate
			}
		}
		hotels = append(hotels, HotelData{
			HotelID:    i + 1,
			HotelName:  r.HotelName,
			Price:      pricePerNight,
			Currency:   "USD",
			Rating:     r.ReviewScore,
			RatingWord: r.ReviewWordEN,
			PhotoURL:   photo,
			Stars:      stars,
		})
	}
	return hotels, nil
}

// fetchHotelsFromHotelsCom fetches real hotel listings with photos via Hotels.com RapidAPI.
func (pc *ProxyClient) fetchHotelsFromHotelsCom(regionID, checkIn, checkOut, adults, rooms string) ([]HotelData, error) {
	if regionID == "" {
		return nil, fmt.Errorf("no region ID")
	}
	resp, err := pc.doGet(hotelsBase, hotelsHost, "/v2/hotels/search", map[string]string{
		"region_id":      regionID,
		"locale":         "en_US",
		"check_in_date":  checkIn,
		"check_out_date": checkOut,
		"adults_number":  adults,
		"rooms_number":   rooms,
		"domain":         "US",
		"sort_order":     "STAR_RATING_HIGHEST_FIRST",
	})
	if err != nil {
		return nil, fmt.Errorf("hotels.com search failed: %w", err)
	}
	defer resp.Body.Close()

	var payload hcHotelSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode hotels.com response: %w", err)
	}

	results := payload.Data.Body.SearchResults.Results
	if len(results) == 0 {
		return nil, fmt.Errorf("hotels.com returned no results")
	}

	hotels := make([]HotelData, 0, len(results))
	for i, r := range results {
		photo := r.OptimizedThumbUrls.SrpDesktop
		if photo == "" {
			photo = r.ThumbnailURL
		}
		rating := r.GuestReviews.UnformattedRating
		stars := r.StarRating
		if stars == 0 {
			stars = 3
		}
		hotels = append(hotels, HotelData{
			HotelID:    i + 1,
			HotelName:  r.Name,
			Price:      r.RatePlan.Price.ExactCurrent,
			Currency:   "USD",
			Rating:     rating,
			RatingWord: amadeusRatingWord(rating),
			PhotoURL:   photo,
			Stars:      stars,
		})
	}
	return hotels, nil
}

// FetchHotels tries Booking.com first for real data with photos, falls back to mock.
func (pc *ProxyClient) FetchHotels(city, regionID, checkIn, checkOut, adults, children, rooms string) ([]HotelData, error) {
	hotels, err := pc.fetchHotelsFromBooking(city, checkIn, checkOut, adults, rooms)
	if err != nil {
		log.Printf("booking.com fetch failed (%v), using mock", err)
		return pc.fetchMockHotels(city)
	}
	return hotels, nil
}

// FetchTravelData is kept for the /travel-data JSON endpoint (always mock).
func (pc *ProxyClient) FetchTravelData() ([]HotelData, error) {
	return pc.fetchMockHotels("London")
}

// fetchFlightsGF searches flights via Google Flights (fallback).
func (pc *ProxyClient) fetchFlightsGF(fromSkyID, toSkyID, date, returnDate, adults, children, cabinClass string) ([]FlightData, error) {
	params := map[string]string{
		"departure_id":  fromSkyID,
		"arrival_id":    toSkyID,
		"outbound_date": date,
		"travel_class":  strings.ToUpper(cabinClass),
		"adults":        adults,
		"currency":      "USD",
		"search_type":   "cheap",
		"language_code": "en-US",
		"country_code":  "US",
	}
	if children != "" && children != "0" {
		params["children"] = children
	}
	if returnDate != "" {
		params["return_date"] = returnDate
	}
	resp, err := pc.doGet(gfBase, gfHost, "/api/v1/searchFlights", params)
	if err != nil {
		return nil, fmt.Errorf("gf flight search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("[GF] status=%d body_prefix=%.300s", resp.StatusCode, string(body))

	var payload gfFlightResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("gf flight decode: %w", err)
	}

	all := append(payload.Data.Itineraries.TopFlights, payload.Data.Itineraries.OtherFlights...)
	if len(all) == 0 {
		return nil, fmt.Errorf("google flights: no itineraries")
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Price < all[j].Price })
	flights := make([]FlightData, 0, len(all))
	for _, it := range all {
		if len(it.Flights) == 0 {
			continue
		}
		first := it.Flights[0]
		last := it.Flights[len(it.Flights)-1]
		dep := it.DepartureTime
		arr := it.ArrivalTime
		if len(dep) >= 16 {
			dep = dep[11:16]
		}
		if len(arr) >= 16 {
			arr = arr[11:16]
		}
		flights = append(flights, FlightData{
			FromCode:        first.DepartureAirport.AirportCode,
			ToCode:          last.ArrivalAirport.AirportCode,
			DepartTime:      dep,
			ArriveTime:      arr,
			DurationHours:   it.Duration.Raw / 60,
			DurationMinutes: it.Duration.Raw % 60,
			Airline:         first.Airline,
			AirlineLogo:     it.AirlineLogo,
			Price:           it.Price,
			Currency:        "USD",
			Stops:           len(it.Layovers),
		})
	}
	return flights, nil
}

// FetchFlights tries Skyscanner first (shows LCC prices), falls back to Google Flights.
func (pc *ProxyClient) FetchFlights(fromSkyID, fromEntityID, toSkyID, toEntityID, date, returnDate, adults, children, cabinClass string) ([]FlightData, error) {
	if adults == "" {
		adults = "1"
	}
	if cabinClass == "" {
		cabinClass = "economy"
	}

	// Resolve Skyscanner entity IDs (numeric) from IATA codes
	fromID := fromEntityID
	if fromID == "" || fromID == fromSkyID {
		fromID = pc.resolveSkyscannerEntityID(fromSkyID)
	}
	toID := toEntityID
	if toID == "" || toID == toSkyID {
		toID = pc.resolveSkyscannerEntityID(toSkyID)
	}
	log.Printf("[FLY] from=%s(%s) to=%s(%s) date=%s return=%s", fromSkyID, fromID, toSkyID, toID, date, returnDate)

	endpoint := "/flights/search-one-way"
	if returnDate != "" {
		endpoint = "/flights/search-roundtrip"
	}

	params := map[string]string{
		"fromEntityId": fromID,
		"toEntityId":   toID,
		"departDate":   date,
		"market":       "US",
		"locale":       "en-US",
		"currency":     "USD",
		"adults":       adults,
		"cabinClass":   cabinClass,
	}
	if returnDate != "" {
		params["returnDate"] = returnDate
	}
	if children != "" && children != "0" {
		params["children"] = children
	}

	resp, err := pc.doGet(skyBase, skyHost, endpoint, params)
	if err != nil {
		log.Printf("[SKY] http error: %v", err)
	} else {
		defer resp.Body.Close()
		skyBody, _ := io.ReadAll(resp.Body)
		log.Printf("[SKY] status=%d body_prefix=%.300s", resp.StatusCode, string(skyBody))
		var payload skyFlightResponse
		if json.Unmarshal(skyBody, &payload) == nil && payload.Status && len(payload.Data.Itineraries) > 0 {
			its := payload.Data.Itineraries
			sort.Slice(its, func(i, j int) bool { return its[i].Price.Raw < its[j].Price.Raw })
			flights := make([]FlightData, 0, len(its))
			for _, it := range its {
				if len(it.Legs) == 0 {
					continue
				}
				leg := it.Legs[0]
				airline, logo := "", ""
				if len(leg.Carriers.Marketing) > 0 {
					airline = leg.Carriers.Marketing[0].Name
					logo = leg.Carriers.Marketing[0].LogoURL
				}
				flights = append(flights, FlightData{
					FromCode: leg.Origin.DisplayCode, ToCode: leg.Destination.DisplayCode,
					DepartTime: extractISOTime(leg.Departure), ArriveTime: extractISOTime(leg.Arrival),
					DurationHours: leg.DurationInMinutes / 60, DurationMinutes: leg.DurationInMinutes % 60,
					Airline: airline, AirlineLogo: logo,
					Price: it.Price.Raw, Currency: "USD", Stops: leg.StopCount,
				})
			}
			if len(flights) > 0 {
				return flights, nil
			}
		}
	}

	// Fallback: Google Flights
	log.Printf("[SKY] failed for %s→%s, trying google flights", fromSkyID, toSkyID)
	return pc.fetchFlightsGF(fromSkyID, toSkyID, date, returnDate, adults, children, cabinClass)
}

// extractISOTime pulls HH:MM from a Skyscanner ISO timestamp like "2026-06-15T11:25:00".
func extractISOTime(s string) string {
	if idx := strings.Index(s, "T"); idx >= 0 && len(s) > idx+6 {
		return s[idx+1 : idx+6]
	}
	return s
}

package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("Search is temporarily unavailable. Please try again later.")
var ErrQuota = errors.New("Search is temporarily unavailable because the provider's search limit has been reached.")

type serpResult struct {
	Metadata struct {
		Status     string `json:"status"`
		FlightsURL string `json:"google_flights_url"`
		HotelsURL  string `json:"google_hotels_url"`
	} `json:"search_metadata"`
	Error      string       `json:"error"`
	Best       []serpFlight `json:"best_flights"`
	Other      []serpFlight `json:"other_flights"`
	Properties []serpHotel  `json:"properties"`
}
type serpFlight struct {
	Price    float64 `json:"price"`
	Duration int     `json:"total_duration"`
	Logo     string  `json:"airline_logo"`
	Flights  []struct {
		Departure struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Time string `json:"time"`
		} `json:"departure_airport"`
		Arrival struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Time string `json:"time"`
		} `json:"arrival_airport"`
		Airline string `json:"airline"`
		Logo    string `json:"airline_logo"`
	} `json:"flights"`
}
type serpHotel struct {
	Name   string  `json:"name"`
	Link   string  `json:"link"`
	Rating float64 `json:"overall_rating"`
	Stars  int     `json:"extracted_hotel_class"`
	Rate   struct {
		Price float64 `json:"extracted_lowest"`
	} `json:"rate_per_night"`
	Total struct {
		Price float64 `json:"extracted_lowest"`
	} `json:"total_rate"`
	Images []struct {
		Thumbnail string `json:"thumbnail"`
	} `json:"images"`
}

func (pc *ProxyClient) search(q url.Values) (serpResult, error) {
	var data serpResult
	if pc.apiKey == "" {
		return data, ErrUnavailable
	}
	q.Set("api_key", pc.apiKey)
	q.Set("currency", "USD")
	q.Set("hl", "en")
	q.Set("gl", "us")
	// SerpApi's native cache stays enabled. No retries, polling or pagination.
	resp, err := pc.HTTPClient.Get("https://serpapi.com/search.json?" + q.Encode())
	if err != nil {
		log.Printf("SerpApi %s: network error (URL withheld)", q.Get("engine"))
		return data, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("SerpApi %s: HTTP %d", q.Get("engine"), resp.StatusCode)
		if resp.StatusCode == http.StatusTooManyRequests {
			return data, ErrQuota
		}
		return data, ErrUnavailable
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&data); err != nil {
		return data, ErrUnavailable
	}
	if data.Error != "" {
		// Do not log raw upstream errors: they may contain credentials or request URLs.
		if strings.Contains(strings.ToLower(data.Error), "hasn't returned any results") {
			return serpResult{}, nil
		}
		return serpResult{}, ErrUnavailable
	}
	if data.Metadata.Status != "Success" {
		return serpResult{}, ErrUnavailable
	}
	return data, nil
}

func dateValue(value string) (time.Time, error) {
	d, err := time.Parse("2006-01-02", value)
	if err != nil || value < time.Now().Format("2006-01-02") {
		return d, errors.New("Please choose a valid date today or in the future.")
	}
	return d, nil
}
func count(value string, fallback, min, max int) (string, error) {
	if value == "" {
		value = strconv.Itoa(fallback)
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return "", errors.New("Please check the number of travellers.")
	}
	return strconv.Itoa(n), nil
}
func safeLink(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "serpapi.com" || strings.HasSuffix(host, ".serpapi.com") || u.Query().Has("api_key") {
		return ""
	}
	return u.String()
}

func (pc *ProxyClient) FetchHotels(city, regionID, checkIn, checkOut, adults, children, rooms string, ages ...string) ([]HotelData, error) {
	city = strings.TrimSpace(city)
	if city == "" || len(city) > 200 {
		return nil, errors.New("Please enter a destination.")
	}
	start, err := dateValue(checkIn)
	if err != nil {
		return nil, err
	}
	end, err := dateValue(checkOut)
	if err != nil {
		return nil, err
	}
	if !end.After(start) {
		return nil, errors.New("Check-out must be after check-in.")
	}
	if rooms != "" && rooms != "1" {
		return nil, errors.New("Hotel search currently supports one room at a time.")
	}
	adults, err = count(adults, 2, 1, 9)
	if err != nil {
		return nil, err
	}
	children, err = count(children, 0, 0, 6)
	if err != nil {
		return nil, err
	}
	q := url.Values{"engine": {"google_hotels"}, "q": {city + " hotels"}, "check_in_date": {checkIn}, "check_out_date": {checkOut}, "adults": {adults}, "children": {children}}
	if children != "0" {
		raw := ""
		if len(ages) > 0 {
			raw = ages[0]
		}
		parts := strings.Split(raw, ",")
		n, _ := strconv.Atoi(children)
		if len(parts) != n {
			return nil, errors.New("Please select an age for each child.")
		}
		for i, p := range parts {
			a, e := strconv.Atoi(p)
			if e != nil || a < 0 || a > 17 {
				return nil, errors.New("Please select an age for each child.")
			}
			if a == 0 {
				a = 1
			}
			parts[i] = strconv.Itoa(a)
		}
		q.Set("children_ages", strings.Join(parts, ","))
	}
	data, err := pc.search(q)
	if err != nil {
		return nil, err
	}
	hotels := []HotelData{}
	for i, h := range data.Properties {
		if h.Name == "" || h.Rate.Price <= 0 {
			continue
		}
		photo := ""
		if len(h.Images) > 0 {
			photo = safeLink(h.Images[0].Thumbnail)
		}
		link := safeLink(h.Link)
		if link == "" {
			link = safeLink(data.Metadata.HotelsURL)
		}
		if link == "" {
			link = "https://www.google.com/travel/hotels?" + url.Values{"q": {h.Name + " " + city}}.Encode()
		}
		ratingWord := ""
		if h.Rating > 0 {
			ratingWord = fmt.Sprintf("Google rating: %.1f/5", h.Rating)
		}
		hotels = append(hotels, HotelData{HotelID: i + 1, HotelName: h.Name, Price: h.Rate.Price, TotalPrice: h.Total.Price, Currency: "USD", Rating: h.Rating * 2, RatingWord: ratingWord, Stars: h.Stars, PhotoURL: photo, BookingURL: link})
	}
	return hotels, nil
}

type FlightSearchLeg struct {
	From string `json:"departure_id"`
	To   string `json:"arrival_id"`
	Date string `json:"date"`
}

func airport(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}
func (pc *ProxyClient) FetchFlights(from, fromEntity, to, toEntity, date, returnDate, adults, children, cabin string, ages ...string) ([]FlightData, error) {
	return pc.FetchItinerary([]FlightSearchLeg{{From: strings.ToUpper(strings.TrimSpace(from)), To: strings.ToUpper(strings.TrimSpace(to)), Date: date}}, returnDate, adults, children, cabin, ages...)
}
func (pc *ProxyClient) FetchItinerary(legs []FlightSearchLeg, returnDate, adults, children, cabin string, ages ...string) ([]FlightData, error) {
	if len(legs) < 1 || len(legs) > 6 {
		return nil, errors.New("Choose between one and six flight legs.")
	}
	previous := ""
	for _, leg := range legs {
		if !airport(leg.From) || !airport(leg.To) || leg.From == leg.To {
			return nil, errors.New("Enter valid three-letter airport codes, such as BER and LHR.")
		}
		if _, err := dateValue(leg.Date); err != nil {
			return nil, err
		}
		if leg.Date < previous {
			return nil, errors.New("Flight dates must be in chronological order.")
		}
		previous = leg.Date
	}
	var err error
	adults, err = count(adults, 1, 1, 9)
	if err != nil {
		return nil, err
	}
	children, err = count(children, 0, 0, 8)
	if err != nil {
		return nil, err
	}
	a, _ := strconv.Atoi(adults)
	c, _ := strconv.Atoi(children)
	if a+c > 9 {
		return nil, errors.New("Search supports up to nine travellers.")
	}
	classes := map[string]string{"": "1", "economy": "1", "premium_economy": "2", "business": "3", "first": "4"}
	class, ok := classes[cabin]
	if !ok {
		return nil, errors.New("Please choose a valid cabin class.")
	}
	q := url.Values{"engine": {"google_flights"}, "adults": {adults}, "children": {children}, "travel_class": {class}}
	if len(ages) > 0 && c > 0 {
		parts := strings.Split(ages[0], ",")
		if len(parts) != c {
			return nil, errors.New("Please select an age for each child.")
		}
		infants := 0
		for _, part := range parts {
			age, err := strconv.Atoi(part)
			if err != nil || age < 0 || age > 17 {
				return nil, errors.New("Please select an age for each child.")
			}
			if age < 2 {
				infants++
			}
		}
		if infants > a {
			return nil, errors.New("Each infant needs an accompanying adult.")
		}
		q.Set("children", strconv.Itoa(c-infants))
		if infants > 0 {
			q.Set("infants_on_lap", strconv.Itoa(infants))
		}
	}
	label := "one-way total · all travellers"
	if len(legs) > 1 {
		encoded, _ := json.Marshal(legs)
		q.Set("type", "3")
		q.Set("multi_city_json", string(encoded))
		label = "multi-city estimate · all travellers · first leg shown"
	} else {
		q.Set("departure_id", legs[0].From)
		q.Set("arrival_id", legs[0].To)
		q.Set("outbound_date", legs[0].Date)
		q.Set("type", "2")
		if returnDate != "" {
			if _, err := dateValue(returnDate); err != nil {
				return nil, err
			}
			if returnDate < legs[0].Date {
				return nil, errors.New("Return date must not precede departure.")
			}
			q.Set("type", "1")
			q.Set("return_date", returnDate)
			label = "round-trip estimate · all travellers · outbound shown"
		}
	}
	data, err := pc.search(q)
	if err != nil {
		return nil, err
	}
	link := safeLink(data.Metadata.FlightsURL)
	if link == "" {
		link = "https://www.google.com/travel/flights"
	}
	flights := []FlightData{}
	for _, f := range append(data.Best, data.Other...) {
		if len(f.Flights) == 0 || f.Price <= 0 {
			continue
		}
		first, last := f.Flights[0], f.Flights[len(f.Flights)-1]
		dep, e1 := time.Parse("2006-01-02 15:04", first.Departure.Time)
		arr, e2 := time.Parse("2006-01-02 15:04", last.Arrival.Time)
		if e1 != nil || e2 != nil {
			continue
		}
		logo := safeLink(f.Logo)
		if logo == "" {
			logo = safeLink(first.Logo)
		}
		flights = append(flights, FlightData{FromCity: first.Departure.Name, ToCity: last.Arrival.Name, FromCode: first.Departure.ID, ToCode: last.Arrival.ID, DepartDate: dep.Format("2006-01-02"), DepartTime: dep.Format("15:04"), ArriveTime: arr.Format("15:04"), DurationHours: f.Duration / 60, DurationMinutes: f.Duration % 60, Airline: first.Airline, AirlineLogo: logo, Price: f.Price, Currency: "USD", Stops: len(f.Flights) - 1, BookingURL: link, PriceLabel: label})
	}
	sort.SliceStable(flights, func(i, j int) bool { return flights[i].Price < flights[j].Price })
	return flights, nil
}

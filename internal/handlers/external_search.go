package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

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
func (h *TravelHandler) externalHotels(w http.ResponseWriter, r *http.Request, pd pageData, category string) {
	q := r.URL.Query()
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
	a, c, ages, err := externalGuests(q, true)
	if err != nil {
		fail(err)
		return
	}
	rooms, err := searchCount(pd.Rooms, 1, 1, 9)
	if err != nil {
		fail(err)
		return
	}
	destinations := []string{strings.TrimSpace(pd.City)}
	if category != "" {
		categories := map[string][]string{
			"city": {"London", "Paris", "Berlin"}, "beach": {"Antalya", "Bali", "Maldives"}, "apartments": {"Amsterdam", "Barcelona", "Rome"}, "villas": {"Tuscany", "Bali", "Antalya"}, "budget": {"Prague", "Berlin", "Bangkok"}, "boutique": {"Florence", "Paris", "Istanbul"}, "cabins": {"Lapland", "Swiss Alps", "Tyrol"},
		}
		var ok bool
		destinations, ok = categories[category]
		if !ok {
			fail(fmt.Errorf("Please select a supported hotel category."))
			return
		}
	}
	for _, city := range destinations {
		if city == "" || len(city) > 200 {
			fail(fmt.Errorf("Please enter a destination."))
			return
		}
		params := url.Values{"ss": {city}, "checkin": {pd.Checkin}, "checkout": {pd.Checkout}, "group_adults": {a}, "group_children": {c}, "no_rooms": {rooms}}
		for _, age := range ages {
			params.Add("age", age)
		}
		pd.ExternalLinks = append(pd.ExternalLinks, searchLink{"Search " + city + " on Booking.com", "https://www.booking.com/searchresults.html?" + params.Encode()})
	}
	h.render(w, pd)
}
func (h *TravelHandler) externalFlights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pd := pageData{Tab: "flights", TripType: q.Get("tripType"), FromSkyID: q.Get("fromSky"), ToSkyID: q.Get("toSky"), Date: q.Get("date"), ReturnDate: q.Get("returnDate"), Adults: q.Get("adults"), Children: q.Get("children"), CabinClass: q.Get("cabinClass")}
	fail := func(err error) { pd.Error = err.Error(); h.render(w, pd) }
	if pd.TripType == "" {
		pd.TripType = "oneway"
	}
	if pd.CabinClass == "" {
		pd.CabinClass = "economy"
	}
	if pd.TripType != "multi" && pd.FromSkyID == "" && pd.ToSkyID == "" {
		h.render(w, pd)
		return
	}
	a, c, ages, err := externalGuests(q, false)
	if err != nil {
		fail(err)
		return
	}
	pd.Adults, pd.Children = a, c
	cabin, ok := map[string]string{"economy": "economy", "premium_economy": "premium economy", "business": "business", "first": "first class"}[pd.CabinClass]
	if !ok {
		fail(fmt.Errorf("Please choose a valid cabin class."))
		return
	}
	var segments []string
	previous := ""
	add := func(from, to, date string) error {
		from = strings.ToUpper(strings.TrimSpace(from))
		to = strings.ToUpper(strings.TrimSpace(to))
		valid := func(code string) bool {
			if len(code) != 3 {
				return false
			}
			for _, v := range code {
				if v < 'A' || v > 'Z' {
					return false
				}
			}
			return true
		}
		if !valid(from) || !valid(to) || from == to {
			return fmt.Errorf("Enter valid three-letter airport codes, such as BER and LHR.")
		}
		if _, err := searchDate(date); err != nil {
			return err
		}
		if date < previous {
			return fmt.Errorf("Flight dates must be in chronological order.")
		}
		previous = date
		segments = append(segments, fmt.Sprintf("from %s to %s on %s", from, to, date))
		return nil
	}
	if pd.TripType == "multi" {
		for i := 0; i < 6; i++ {
			from, to, date := q.Get(fmt.Sprintf("leg%dfrom", i)), q.Get(fmt.Sprintf("leg%dto", i)), q.Get(fmt.Sprintf("leg%ddate", i))
			if from == "" && to == "" && date == "" {
				continue
			}
			if err := add(from, to, date); err != nil {
				fail(err)
				return
			}
		}
		if len(segments) < 2 {
			fail(fmt.Errorf("Please enter at least two complete flight legs."))
			return
		}
	} else {
		if pd.Date == "" {
			pd.Date = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		}
		if err := add(pd.FromSkyID, pd.ToSkyID, pd.Date); err != nil {
			fail(err)
			return
		}
		if pd.ReturnDate != "" || pd.TripType == "round" {
			if err := add(pd.ToSkyID, pd.FromSkyID, pd.ReturnDate); err != nil {
				fail(err)
				return
			}
			pd.TripType = "round"
		} else if pd.TripType != "oneway" {
			fail(fmt.Errorf("Please choose a valid trip type."))
			return
		}
	}
	text := "Flights " + strings.Join(segments, ", ") + fmt.Sprintf(", %s adults, %s children, %s", a, c, cabin)
	if len(ages) > 0 {
		text += ", child ages " + strings.Join(ages, ", ")
	}
	if pd.TripType == "oneway" {
		text += ", one way"
	}
	pd.ExternalLinks = []searchLink{{"Search on Google Flights", "https://www.google.com/travel/flights?" + url.Values{"q": {text}}.Encode()}}
	h.render(w, pd)
}

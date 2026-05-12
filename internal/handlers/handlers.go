package handlers

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"time"

	"travel-proxy-service/internal/proxy"
)

type TravelHandler struct {
	ProxyClient *proxy.ProxyClient
	Templates   *template.Template
}

type pageData struct {
	Tab          string
	City         string
	CityEntityID string
	Checkin      string
	Checkout     string
	FromSkyID    string
	FromEntityID string
	ToSkyID      string
	ToEntityID   string
	FromCity     string
	ToCity       string
	Date         string
	Hotels       []proxy.HotelData
	Flights      []proxy.FlightData
	Error        string
}

func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
}

func (h *TravelHandler) render(w http.ResponseWriter, data pageData) {
	tmpl := h.Templates.Lookup("index.html")
	if tmpl == nil {
		log.Println("template index.html not found")
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("template execution error: %v", err)
	}
}

func (h *TravelHandler) HomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	city := r.URL.Query().Get("q")
	entityID := r.URL.Query().Get("entityId")
	if city == "" {
		city = ""
		h.render(w, pageData{Tab: "hotels"})
		return
	}

	checkin := r.URL.Query().Get("checkin")
	if checkin == "" {
		checkin = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	}
	checkout := r.URL.Query().Get("checkout")
	if checkout == "" {
		checkout = time.Now().AddDate(0, 0, 5).Format("2006-01-02")
	}

	pd := pageData{Tab: "hotels", City: city, Checkin: checkin, Checkout: checkout}

	if entityID == "" {
		var err error
		entityID, err = h.ProxyClient.SearchHotelDestination(city)
		if err != nil {
			pd.Error = "Destination not found: " + city
			h.render(w, pd)
			return
		}
	}
	pd.CityEntityID = entityID

	hotels, err := h.ProxyClient.FetchHotels(entityID, checkin, checkout)
	if err != nil {
		pd.Error = "Failed to load hotels. Please try again."
		h.render(w, pd)
		return
	}

	pd.Hotels = hotels
	h.render(w, pd)
}

func (h *TravelHandler) FlightsHandler(w http.ResponseWriter, r *http.Request) {
	fromSkyID := r.URL.Query().Get("fromSky")
	fromEntityID := r.URL.Query().Get("fromEntity")
	toSkyID := r.URL.Query().Get("toSky")
	toEntityID := r.URL.Query().Get("toEntity")
	date := r.URL.Query().Get("date")

	// defaults: London Heathrow → Paris CDG
	if fromSkyID == "" {
		fromSkyID = "LHR"
	}
	if fromEntityID == "" {
		fromEntityID = "27544008"
	}
	if toSkyID == "" {
		toSkyID = "CDG"
	}
	if toEntityID == "" {
		toEntityID = "27539733"
	}
	if date == "" {
		date = time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	}

	// If entityIDs are missing, look them up from the skyId
	if fromEntityID == "" {
		if airports, err := h.ProxyClient.SearchAirports(fromSkyID); err == nil {
			for _, a := range airports {
				if a.SkyID == fromSkyID {
					fromEntityID = a.EntityID
					break
				}
			}
			if fromEntityID == "" && len(airports) > 0 {
				fromEntityID = airports[0].EntityID
			}
		}
	}
	if toEntityID == "" {
		if airports, err := h.ProxyClient.SearchAirports(toSkyID); err == nil {
			for _, a := range airports {
				if a.SkyID == toSkyID {
					toEntityID = a.EntityID
					break
				}
			}
			if toEntityID == "" && len(airports) > 0 {
				toEntityID = airports[0].EntityID
			}
		}
	}

	pd := pageData{
		Tab:          "flights",
		FromSkyID:    fromSkyID,
		FromEntityID: fromEntityID,
		ToSkyID:      toSkyID,
		ToEntityID:   toEntityID,
		Date:         date,
	}

	flights, err := h.ProxyClient.FetchFlights(fromSkyID, fromEntityID, toSkyID, toEntityID, date)
	if err != nil {
		pd.Error = "Failed to load flights. Please try again."
		h.render(w, pd)
		return
	}

	pd.Flights = flights
	if len(flights) > 0 {
		pd.FromCity = flights[0].FromCode
		pd.ToCity = flights[0].ToCode
	}
	h.render(w, pd)
}

func (h *TravelHandler) SuggestHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	results, err := h.ProxyClient.SearchHotelDestinations(q)
	if err != nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	json.NewEncoder(w).Encode(results)
}

func (h *TravelHandler) SuggestFlightHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query().Get("q")
	if len(q) < 2 {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	results, err := h.ProxyClient.SearchAirports(q)
	if err != nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	json.NewEncoder(w).Encode(results)
}

func (h *TravelHandler) GetTravelDataHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	hotels, err := h.ProxyClient.FetchTravelData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if err := json.NewEncoder(w).Encode(hotels); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

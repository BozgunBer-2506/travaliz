package handlers

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"

	"travel-proxy-service/internal/proxy"
)

type TravelHandler struct {
	ProxyClient *proxy.ProxyClient
	Templates   *template.Template
}

type pageData struct {
	Tab      string
	City     string
	Checkin  string
	Checkout string
	FromID   string
	ToID     string
	Date     string
	Hotels   []proxy.HotelData
	Flights  []proxy.FlightData
	Error    string
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
	if city == "" {
		city = "London"
	}
	checkin := r.URL.Query().Get("checkin")
	if checkin == "" {
		checkin = "2026-06-01"
	}
	checkout := r.URL.Query().Get("checkout")
	if checkout == "" {
		checkout = "2026-06-05"
	}

	pd := pageData{Tab: "hotels", City: city, Checkin: checkin, Checkout: checkout}

	destID, searchType, err := h.ProxyClient.SearchDestination(city)
	if err != nil {
		pd.Error = "Destination not found: " + city
		h.render(w, pd)
		return
	}

	hotels, err := h.ProxyClient.FetchHotels(destID, searchType, checkin, checkout)
	if err != nil {
		pd.Error = "Failed to load hotels. Please try again."
		h.render(w, pd)
		return
	}

	pd.Hotels = hotels
	h.render(w, pd)
}

func (h *TravelHandler) FlightsHandler(w http.ResponseWriter, r *http.Request) {
	fromID := r.URL.Query().Get("from")
	toID := r.URL.Query().Get("to")
	date := r.URL.Query().Get("date")

	if fromID == "" {
		fromID = "LHR.AIRPORT"
	}
	if toID == "" {
		toID = "CDG.AIRPORT"
	}
	if date == "" {
		date = "2026-06-01"
	}

	pd := pageData{Tab: "flights", FromID: fromID, ToID: toID, Date: date}

	flights, err := h.ProxyClient.FetchFlights(fromID, toID, date)
	if err != nil {
		pd.Error = "Failed to load flights. Please try again."
		h.render(w, pd)
		return
	}

	pd.Flights = flights
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
	results, err := h.ProxyClient.SearchDestinations(q)
	if err != nil {
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	json.NewEncoder(w).Encode(results)
}

func (h *TravelHandler) GetTravelDataHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	city := r.URL.Query().Get("q")
	checkin := r.URL.Query().Get("checkin")
	checkout := r.URL.Query().Get("checkout")

	var hotels []proxy.HotelData
	var err error

	if city != "" {
		destID, searchType, destErr := h.ProxyClient.SearchDestination(city)
		if destErr != nil {
			http.Error(w, destErr.Error(), http.StatusBadGateway)
			return
		}
		if checkin == "" {
			checkin = "2026-06-01"
		}
		if checkout == "" {
			checkout = "2026-06-05"
		}
		hotels, err = h.ProxyClient.FetchHotels(destID, searchType, checkin, checkout)
	} else {
		hotels, err = h.ProxyClient.FetchTravelData()
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if err := json.NewEncoder(w).Encode(hotels); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

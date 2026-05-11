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
	Hotels []proxy.TravelData
}

func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
}

func (h *TravelHandler) HomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	hotels, err := h.ProxyClient.FetchTravelData()
	if err != nil {
		http.Error(w, "failed to fetch hotel data", http.StatusBadGateway)
		return
	}

	tmpl := h.Templates.Lookup("index.html")
	if tmpl == nil {
		log.Println("template index.html not found")
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, pageData{Hotels: hotels}); err != nil {
		log.Printf("template execution error: %v", err)
	}
}

func (h *TravelHandler) GetTravelDataHandler(w http.ResponseWriter, r *http.Request) {
	data, err := h.ProxyClient.FetchTravelData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

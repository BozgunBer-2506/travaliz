package handlers

import (
	"encoding/json"
	"net/http"
	"travel-proxy-service/internal/proxy"
)

// TravelHandler struct holds the dependencies for travel-related endpoints.
type TravelHandler struct {
	ProxyClient *proxy.ProxyClient
}

// HealthCheckHandler returns a simple status message.
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
}

// GetTravelDataHandler fetches data via the proxy and returns it to the client.
func (h *TravelHandler) GetTravelDataHandler(w http.ResponseWriter, r *http.Request) {
	data, err := h.ProxyClient.FetchTravelData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(data)
	if err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
}

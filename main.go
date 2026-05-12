package main

import (
	"html/template"
	"log"
	"net/http"

	"travel-proxy-service/internal/handlers"
	"travel-proxy-service/internal/middleware"
	"travel-proxy-service/internal/proxy"
)

func main() {
	tmpl, err := template.ParseGlob("templates/*.html")
	if err != nil {
		log.Fatalf("failed to parse templates: %v", err)
	}

	proxyClient := proxy.NewProxyClient("")

	travelHandler := &handlers.TravelHandler{
		ProxyClient: proxyClient,
		Templates:   tmpl,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", travelHandler.HomeHandler)
	mux.HandleFunc("/flights", travelHandler.FlightsHandler)
	mux.HandleFunc("/suggest", travelHandler.SuggestHandler)
	mux.HandleFunc("/travel-data", travelHandler.GetTravelDataHandler)
	mux.HandleFunc("/status", handlers.HealthCheckHandler)

	log.Println("Starting server on :8080...")
	if err := http.ListenAndServe(":8080", middleware.LoggingMiddleware(mux)); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

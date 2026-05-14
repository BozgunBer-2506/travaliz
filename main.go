package main

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"

	"travel-proxy-service/internal/handlers"
	"travel-proxy-service/internal/middleware"
	"travel-proxy-service/internal/proxy"
)

//go:embed templates/*.html
var templateFiles embed.FS

func main() {
	tmpl, err := template.ParseFS(templateFiles, "templates/*.html")
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
	mux.HandleFunc("/suggest-flight", travelHandler.SuggestFlightHandler)
	mux.HandleFunc("/travel-data", travelHandler.GetTravelDataHandler)
	mux.HandleFunc("/status", handlers.HealthCheckHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Starting server on :%s...", port)
	if err := http.ListenAndServe(":"+port, middleware.LoggingMiddleware(mux)); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

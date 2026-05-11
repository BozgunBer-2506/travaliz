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

	proxyClient := proxy.NewProxyClient("https://jsonplaceholder.typicode.com")

	travelHandler := &handlers.TravelHandler{
		ProxyClient: proxyClient,
		Templates:   tmpl,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", travelHandler.HomeHandler)
	mux.HandleFunc("/status", handlers.HealthCheckHandler)
	mux.HandleFunc("/travel-data", travelHandler.GetTravelDataHandler)

	wrappedMux := middleware.LoggingMiddleware(mux)

	addr := ":8080"
	log.Printf("Starting server on %s...", addr)

	if err := http.ListenAndServe(addr, wrappedMux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

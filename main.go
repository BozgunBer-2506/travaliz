package main

import (
	"log"
	"net/http"
	"travel-proxy-service/internal/handlers"
	"travel-proxy-service/internal/middleware"
	"travel-proxy-service/internal/proxy"
)

func main() {
	// Initialize the Proxy Client with a dummy API URL
	// Using JSONPlaceholder as a mock travel API for demonstration
	proxyClient := proxy.NewProxyClient("https://jsonplaceholder.typicode.com")

	// Initialize Handlers
	travelHandler := &handlers.TravelHandler{
		ProxyClient: proxyClient,
	}

	// Create a new ServeMux
	mux := http.NewServeMux()

	// Register Routes
	mux.HandleFunc("/status", handlers.HealthCheckHandler)
	mux.HandleFunc("/travel-data", travelHandler.GetTravelDataHandler)

	// Apply Logging Middleware
	wrappedMux := middleware.LoggingMiddleware(mux)

	// Define Server Address
	addr := ":8080"
	log.Printf("Starting server on %s...", addr)

	// Start the Server
	err := http.ListenAndServe(addr, wrappedMux)
	if err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

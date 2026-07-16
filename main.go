package main

import (
	"context"
	"embed"
	"html/template"
	"log"
	"net/http"
	"os"

	"travel-proxy-service/internal/auth"
	"travel-proxy-service/internal/db"
	"travel-proxy-service/internal/handlers"
	"travel-proxy-service/internal/middleware"
	"travel-proxy-service/internal/proxy"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed images/*
var imageFiles embed.FS

//go:embed static/*
var staticFiles embed.FS

func main() {
	tmpl, err := template.ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		log.Fatalf("failed to parse templates: %v", err)
	}

	if err := auth.Init(context.Background()); err != nil {
		log.Fatalf("failed to initialize auth: %v", err)
	}

	database, err := db.Open(os.Getenv("SUPABASE_URL"), os.Getenv("SUPABASE_SECRET_KEY"))
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	proxyClient := proxy.NewProxyClient("")

	travelHandler := &handlers.TravelHandler{
		ProxyClient: proxyClient,
		Templates:   tmpl,
		DB:          database,
	}

	mux := http.NewServeMux()
	mux.Handle("/images/", http.FileServerFS(imageFiles))
	mux.Handle("/static/", http.FileServerFS(staticFiles))
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		http.ServeFileFS(w, r, staticFiles, "static/manifest.json")
	})
	mux.HandleFunc("/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		http.ServeFileFS(w, r, staticFiles, "static/sw.js")
	})
	mux.HandleFunc("/", travelHandler.HomeHandler)
	mux.HandleFunc("/flights", travelHandler.FlightsHandler)
	mux.HandleFunc("/cars", travelHandler.CarsHandler)
	mux.HandleFunc("/book", travelHandler.BookHandler)
	mux.HandleFunc("/suggest", travelHandler.SuggestHandler)
	mux.HandleFunc("/suggest-flight", travelHandler.SuggestFlightHandler)
	mux.HandleFunc("/travel-data", travelHandler.GetTravelDataHandler)
	mux.HandleFunc("/contact", travelHandler.ContactHandler)
	mux.HandleFunc("/my-bookings", travelHandler.MyBookingsHandler)
	mux.HandleFunc("/account", travelHandler.AccountHandler)
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

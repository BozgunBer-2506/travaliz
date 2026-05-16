# travaliz.com

A full-stack travel booking platform built with Go. Search hotels, flights, and car rentals with a modern premium UI.

## Features

- **Hotels** - Search by destination with live autocomplete (Hotels.com API)
- **Flights** - One-way, round-trip, and multi-city search (Google Flights API)
- **Cars** - Car rental search with deep-link to Rentalcars.com
- **Real bookings** - 3-step booking form (passenger details, payment, confirmation) backed by SQLite
- **Booking history** - Local drawer showing all confirmed bookings with reference numbers

## Tech Stack

- **Backend:** Go 1.22, `net/http`, `html/template`
- **Database:** SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **APIs:** Google Flights (`google-flights2.p.rapidapi.com`), Hotels.com Provider (`hotels-com-provider.p.rapidapi.com`)
- **Frontend:** Tailwind CSS (CDN), Inter font, vanilla JS

## Project Structure

```
travel-proxy-service/
├── main.go
├── go.mod
├── bookings.db              # SQLite database (auto-created)
├── templates/
│   └── index.html           # Full UI - hero, search forms, results, booking modal
└── internal/
    ├── handlers/
    │   ├── handlers.go      # Hotel, flight, car, autocomplete handlers
    │   └── booking_handler.go  # POST /book - validates and stores bookings
    ├── db/
    │   └── db.go            # SQLite open, migrate, CreateBooking, GetBookingByRef
    ├── middleware/
    │   └── logging.go       # Request logging
    └── proxy/
        └── client.go        # RapidAPI HTTP client for flights, hotels, airports
```

## Run Locally

```bash
go run .
```

Server starts on port **8080** (or `$PORT` env var). Open `http://localhost:8080`.

```bash
# Custom port or DB path
PORT=3000 DB_PATH=/tmp/bookings.db go run .
```

## Endpoints

| Method | Path              | Description                             |
| ------ | ----------------- | --------------------------------------- |
| GET    | `/`               | Hotel search + landing page             |
| GET    | `/flights`        | Flight search (one-way, round, multi)   |
| GET    | `/cars`           | Car rental search + landing page        |
| GET    | `/suggest`        | Hotel destination autocomplete (JSON)   |
| GET    | `/suggest-flight` | Airport autocomplete (JSON)             |
| POST   | `/book`           | Create booking, returns `TM-XXXXXX` ref |
| GET    | `/status`         | Health check `{"status":"OK"}`          |

## Environment Variables

| Variable  | Default       | Description               |
| --------- | ------------- | ------------------------- |
| `PORT`    | `8080`        | HTTP listen port          |
| `DB_PATH` | `bookings.db` | SQLite database file path |

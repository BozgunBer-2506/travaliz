# travaliz.com

A full-stack travel booking platform built with Go. Search hotels, flights, and car rentals with a modern premium UI.

## Search mode

The default `SEARCH_MODE=external` keeps search forms on Travaliz and displays
links to Booking.com (hotels) and Google Flights (flights). These searches make
no server-side travel API calls and need no API credentials or additional services.
Booking.com links carry destination, dates, rooms, guests and child ages.
Google Flights links carry an encoded natural-language itinerary, cabin and guest
information. Google may interpret these details differently: users must confirm
all search settings on the provider website. Multi-city and category searches are
supported; category links select destinations, not guaranteed property filters.
Provider URL formats are not versioned APIs and can change. External provider
rendering has not been verified in an interactive browser in this environment.
No affiliate account or commission tracking is configured.

Set `SEARCH_MODE=serpapi` explicitly to restore the legacy API results described
below. Deploy the updated application for the new default to take effect.

## Features

- **Hotels** - SerpApi Google Hotels search with offline city suggestions (one room per search)
- **Flights** - SerpApi Google Flights search: one-way, round-trip and multi-city estimates
- **Cars** - Not yet available; no generated vehicle offers are displayed
- **Search results** - Continue to the provider website to confirm availability and book; SerpApi does not issue tickets or reservations
- **Booking history** - Local drawer showing all confirmed bookings with reference numbers

## Tech Stack

- **Backend:** Go 1.22, `net/http`, `html/template`
- **Database:** SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **Search API:** SerpApi Google Flights and Google Hotels (`serpapi.com`)
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
        └── client.go        # SerpApi client and shared result types
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

### Search configuration

Set `SERPAPI_API_KEY` in the **server environment** (Vercel project environment
variables or your process environment). Never expose it to browser code or commit
it. The production application reads environment variables; it does not load
`.env` automatically. Docker Compose passes this setting through from `.env`.
A missing key produces an unavailable message without falling back to another API.

Each city or flight search sends one synchronous SerpApi request, including
multi-city itineraries. Property category searches query three fixed destinations
and interleave their results; each category click can consume three API searches. There are no automatic retries, result pagination or paid autocomplete
calls. SerpApi's native cache stays enabled; there is no added cache service.
Suggestions use a small local starter list; cities can be typed freely and airport
codes can be entered directly. Distinct searches still consume the provider quota.

Hotel search supports one room and requires ages for children. Results without a
price are omitted. Google ratings are scaled from 5 to 10 for the existing filters.
Round-trip/multi-city cards show the outbound/first leg and an itinerary price
estimate for all travellers. Subsequent flight selection happens on Google Flights,
not through additional API calls. Provider links are not affiliate links and do not
guarantee the displayed price. `/travel-data` now returns an empty list, not mock
hotels. Existing account/booking-history functionality is not a SerpApi booking API.

### Optional SerpApi evaluation

The isolated Go command below evaluates Google Hotels (Berlin, two nights) and
Google Flights (BER to LHR, one way) independently of the web server.
It adds no dependencies. First inspect the queries without making API calls:

```bash
go run ./cmd/serpapi-check
```

For a live evaluation, set `SERPAPI_API_KEY` in the environment or the ignored local
`.env` file, then explicitly run:

```bash
go run ./cmd/serpapi-check -live
```

This sends at most two searches and stops at the first error. It may consume free
quota; there are no retries, autocomplete calls, pagination, or booking requests.
Use `-date YYYY-MM-DD` for a specific future departure/check-in date. The default
is 30 days ahead. SerpApi's default cache remains enabled. Output contains result
counts and lowest returned prices, not raw responses or credentials. This is a
provider smoke test, not a booking integration or a guarantee of price availability.

Official references: [pricing](https://serpapi.com/pricing),
[flights](https://serpapi.com/google-flights-api),
[hotels](https://serpapi.com/google-hotels-api).

| Variable  | Default       | Description               |
| --------- | ------------- | ------------------------- |
| `PORT`    | `8080`        | HTTP listen port          |
| `DB_PATH` | `bookings.db` | SQLite database file path |

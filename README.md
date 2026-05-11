# TravelMirror - travel-proxy-service

A Go web application that proxies a REST API and renders the data as a hotel booking UI using Go templates and Tailwind CSS.

## Project Structure

```
travel-proxy-service/
├── go.mod
├── main.go
├── templates/
│   └── index.html          # Tailwind CSS hotel card UI
└── internal/
    ├── handlers/
    │   └── handlers.go     # HTTP route handlers + template rendering
    ├── middleware/
    │   └── logging.go      # Request logging middleware
    └── proxy/
        └── client.go       # Upstream HTTP client + price/rating simulation
```

## Requirements

- Go 1.20 or later

## Build

```bash
go build -o travel-proxy-service .
```

## Run

```bash
go run .
```

The server starts on port **8080**. Open `http://localhost:8080` in your browser.

## Endpoints

| Method | Path           | Description                                        |
|--------|----------------|----------------------------------------------------|
| GET    | `/`            | Hotel listing UI (HTML, rendered from template)    |
| GET    | `/status`      | Health check - returns `{"status":"OK"}`           |
| GET    | `/travel-data` | Raw JSON from upstream API (enriched with price/rating) |

## Upstream Target

Proxied to `https://jsonplaceholder.typicode.com/posts`.

Fields are mapped as:
- `title` - Hotel Name
- `body` - Description
- `price` - Simulated nightly rate ($50-$300)
- `rating` - Simulated guest rating (3.0-5.0)

## Example Requests

```bash
# Open hotel UI
open http://localhost:8080

# Health check
curl http://localhost:8080/status

# Raw JSON
curl http://localhost:8080/travel-data
```

## Environment

Tested on Go 1.22, Linux (WSL2).

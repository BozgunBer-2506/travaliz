# travel-proxy-service

A lightweight HTTP proxy service written in Go that forwards requests to an upstream REST API.

## Project Structure

```
travel-proxy-service/
├── go.mod
├── main.go
└── internal/
    ├── handlers/
    │   └── handlers.go     # HTTP route handlers
    ├── middleware/
    │   └── logging.go      # Request logging middleware
    └── proxy/
        └── client.go       # Upstream HTTP client
```

## Requirements

- Go 1.18 or later

## Build

```bash
go build -o travel-proxy-service .
```

## Run

```bash
go run .
```

The server starts on port **8080**.

## Endpoints

| Method | Path           | Description                              |
|--------|----------------|------------------------------------------|
| GET    | `/status`      | Health check - returns `{"status":"OK"}` |
| GET    | `/travel-data` | Fetches posts from the upstream API      |

## Upstream Target

Currently proxied to `https://jsonplaceholder.typicode.com`.

## Example Requests

```bash
# Health check
curl http://localhost:8080/status

# Fetch travel data
curl http://localhost:8080/travel-data
```

## Environment

Tested on Go 1.18+, Linux (WSL2).

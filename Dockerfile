# --- Build stage ---
FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o travel-proxy-service .

# --- Runtime stage ---
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/travel-proxy-service .
COPY --from=builder /app/templates ./templates

RUN mkdir -p /data

ENV PORT=8080
ENV DB_PATH=/data/bookings.db

EXPOSE 8080

CMD ["./travel-proxy-service"]

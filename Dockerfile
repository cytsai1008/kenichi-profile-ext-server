# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Cache module downloads separately from source.
COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /server .

# --- final stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /server /server

# Data volume mount point.
VOLUME ["/data"]

# Both ports are declared; only the relevant one will be bound per container.
EXPOSE 8080 8081

ENTRYPOINT ["/server"]

# Build context is the repository root (see docker-compose.yml).
FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY apps/api ./apps/api
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./apps/api/cmd/api

# Distroless: no shell, runs as non-root. Includes CA certificates, which the
# adapter needs for outbound TLS to UniFi.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/api /api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]

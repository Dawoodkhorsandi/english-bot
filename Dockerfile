# ---- Build stage ----
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Build a static, CGO-free binary (modernc.org/sqlite is pure Go).
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/english-bot .

# ---- Runtime stage ----
FROM alpine:3.20

# CA certificates for outbound HTTPS (Telegram + AI providers). Copied from the
# build image to avoid needing network access during the runtime build.
# Timezone data is embedded in the binary via the `time/tzdata` import, so no
# OS tzdata package is required.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Run as a non-root user; /data holds the SQLite database (mounted volume).
RUN adduser -D -H -u 10001 appuser \
    && mkdir -p /data \
    && chown appuser:appuser /data

COPY --from=build /out/english-bot /app/english-bot

USER appuser
WORKDIR /data

# subscribers.db is created here (relative path), inside the mounted volume.
ENTRYPOINT ["/app/english-bot"]

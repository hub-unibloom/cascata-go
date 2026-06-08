# Cert Controller Sidecar
# Isolated certificate management service

FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Sync go.mod with actual imports and build
RUN go mod tidy
RUN go vet ./...
RUN CGO_ENABLED=0 GOOS=linux go build -v -a -installsuffix cgo -o cert-controller ./cmd/cert-controller/main.go 2>&1

# Final stage - minimal image with certbot
FROM alpine:3.19

# Install required packages
RUN apk add --no-cache \
    certbot \
    certbot-nginx \
    openssl \
    ca-certificates \
    curl \
    && rm -rf /var/cache/apk/*

# Create directories
RUN mkdir -p /etc/letsencrypt /var/www/html /etc/nginx/conf.d/dynamic

# Copy binary from builder
COPY --from=builder /app/cert-controller /usr/local/bin/cert-controller

# Non-root user for security (optional, can be enabled)
# RUN adduser -D -s /bin/sh certbot
# USER certbot

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD pgrep cert-controller || exit 1

# Run the cert-controller
CMD ["/usr/local/bin/cert-controller"]

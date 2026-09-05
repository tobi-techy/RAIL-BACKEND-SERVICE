# Build stage
FROM golang:1.25-alpine AS builder

# Install necessary packages for building
RUN apk add --no-cache git ca-certificates tzdata gcc musl-dev

# Create non-root user for build
RUN adduser -D -s /bin/sh builduser

# Set working directory
WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Cache bust arg - change to force rebuild of subsequent layers
ARG CACHE_BUST=1

# Copy source code
COPY . .

# Change ownership to builduser
RUN chown -R builduser:builduser /app
USER builduser

# Build the application with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o main ./cmd/main.go

# Final stage - minimal runtime image
FROM scratch

# Import ca-certificates from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Import timezone data
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Create minimal filesystem structure
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Copy binary from builder stage
COPY --from=builder /app/main /main

# Create writable tmp directory for pdfcpu and other libs
COPY --from=builder --chown=builduser:builduser /tmp /tmp

# Copy config files
COPY --from=builder /app/configs /configs

# Copy migrations
COPY --from=builder /app/migrations /migrations

# Copy static files (AASA for passkeys)
COPY --from=builder /app/static /static

# Use non-root user
USER builduser

# Expose port — defaults to 8080 in the app (config.go: server.port default 8080,
# overridden by PORT env var). AtlasFlow reads PORT to know where to probe.
EXPOSE 8080

# Add health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
  CMD ["/main", "--health-check"] || exit 1

# Set environment variables for security
ENV GIN_MODE=release
ENV CGO_ENABLED=0
ENV HOME=/tmp

# Run the binary
ENTRYPOINT ["/main"]
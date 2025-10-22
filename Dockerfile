# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the HTTP server binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags=!lambda -ldflags="-w -s" -o /app/bin/enoti ./cmd/enoti

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/bin/enoti /app/enoti

# Expose default port
EXPOSE 8080

# Run the HTTP server
ENTRYPOINT ["/app/enoti"]
CMD ["serve"]

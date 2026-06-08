# Builder image
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Build binary
RUN go build -o /app/edgegrid ./cmd/edgegrid

# Final minimal image
FROM golang:1.24-alpine

# Copy binary from builder
COPY --from=builder /app/edgegrid /edgegrid

# Expose Coordinator HTTP API Port
EXPOSE 8080

CMD ["/edgegrid"]

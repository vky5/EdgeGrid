# Builder image
FROM golang:1.24-alpine AS builder

WORKDIR /app/coordinator

# Copy go.mod and go.sum of coordinator
COPY coordinator/go.mod coordinator/go.sum ./

# Copy shared module's go.mod and go.sum.
COPY shared/go.mod shared/go.sum ../shared/

# Download dependencies
RUN go mod download

# Copy the full source code
COPY coordinator/ ./
COPY shared/ ../shared/

# Build binary
RUN go build -o /app/coordinator/coordinator ./cmd

# Final minimal image
FROM golang:1.24-alpine

# Copy binary from builder
COPY --from=builder /app/coordinator/coordinator /coordinator

EXPOSE 8080

CMD ["/coordinator"]

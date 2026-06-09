FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/edgegrid ./cmd/edgegrid

FROM alpine:3.21

RUN adduser -D -H -u 10001 edgegrid
COPY --from=builder /app/edgegrid /edgegrid

USER edgegrid
EXPOSE 8080

ENTRYPOINT ["/edgegrid"]

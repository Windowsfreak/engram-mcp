# Multi-stage Dockerfile for Engram-MCP
FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/engram ./cmd/engram

FROM alpine:latest

WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/engram /usr/local/bin/engram

# Create storage directory for workspaces
RUN mkdir -p /data/workspaces

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/engram"]
CMD ["serve-http"]

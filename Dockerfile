# Stage 1: Build the Go binary
FROM golang:alpine AS builder

WORKDIR /app

# Install git for module resolution if needed
RUN apk add --no-cache git

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source
COPY . .

# Build with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o autodns .

# Stage 2: Minimal runtime image
FROM alpine:latest

# Required for DNS resolution if your app calls out
RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/autodns .

EXPOSE 53/udp
EXPOSE 53/tcp

ENTRYPOINT ["./autodns"]

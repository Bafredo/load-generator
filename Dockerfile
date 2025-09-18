# Dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/main .

# Set environment variables with defaults
ENV TARGET_URL=https://httpbin.org/get
ENV NUM_WORKERS=400
ENV REQUEST_DELAY=10ms
ENV STATS_INTERVAL=5s

CMD ["./main"]
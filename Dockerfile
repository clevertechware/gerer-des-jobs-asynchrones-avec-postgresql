# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o gerer-ses-jobs-asynchrones-avec-postgresql .

# Runtime stage
FROM alpine:3.18

WORKDIR /app

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates tzdata

# Copy binary from builder
COPY --from=builder /app/gerer-ses-jobs-asynchrones-avec-postgresql .
COPY --from=builder /app/migrations ./migrations

# Create uploads directory
RUN mkdir -p uploads

# Set timezone
ENV TZ=UTC

EXPOSE 8080

CMD ["./gerer-ses-jobs-asynchrones-avec-postgresql"]

# Stage 1: Builder
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags='-w -s' -o movie-server .

# Stage 2: Runner
FROM alpine:latest

# Install tzdata for timezone support and ca-certificates for HTTPS
RUN apk add --no-cache tzdata ca-certificates

ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=builder /app/movie-server .
COPY --from=builder /app/templates ./templates

RUN mkdir -p /app/data /app/logs

EXPOSE 8080

CMD ["./movie-server"]

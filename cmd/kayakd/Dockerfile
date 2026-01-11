FROM golang:1.21-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o kayakd ./cmd/kayakd

FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /build/kayakd /usr/local/bin/kayakd

# Create data directory
RUN mkdir -p /data

EXPOSE 4242/udp

CMD ["kayakd", "--data-dir", "/data", "--listen", "0.0.0.0:4242"]

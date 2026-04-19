FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY logo.png screenshot.png ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /kaffe-letter ./cmd/newsletter

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /kaffe-letter /usr/local/bin/kaffe-letter
ENTRYPOINT ["/usr/local/bin/kaffe-letter"]

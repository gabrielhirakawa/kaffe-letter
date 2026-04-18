FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /newsletter ./cmd/newsletter

FROM alpine:3.20
RUN adduser -D appuser
WORKDIR /app
COPY --from=builder /newsletter /usr/local/bin/newsletter
USER appuser
ENTRYPOINT ["/usr/local/bin/newsletter"]

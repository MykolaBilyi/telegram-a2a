# build stage
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app ./cmd/app/main.go

# final stage
FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /app /app
COPY --from=builder /src/configs/default.yml /default.yml

LABEL Name=telegram-a2a
EXPOSE 8080
CMD ["/app"]

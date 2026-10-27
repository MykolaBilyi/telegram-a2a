# syntax=docker/dockerfile:1
# build stage
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /app ./cmd/app/main.go

# final stage
FROM alpine:3.24
RUN apk --no-cache add ca-certificates
COPY --from=builder /app /app
COPY --from=builder /src/configs/default.yml /default.yml

LABEL Name=telegram-a2a
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["/app"]

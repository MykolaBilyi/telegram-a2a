package middleware

import (
	"telegram-a2a/internal/entities"
	"telegram-a2a/internal/ports"
	"telegram-a2a/internal/providers"
	"telegram-a2a/pkg/middleware"
	"net/http"

	"github.com/lithammer/shortuuid/v4"
)

type RequestIdMiddleware struct {
	header            string
	requestIdProvider providers.RequestIdProvider
	next              http.Handler
}

func (m *RequestIdMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestId := r.Header.Get(m.header)
	if requestId == "" {
		requestId = shortuuid.New()
	}
	m.next.ServeHTTP(w, r.WithContext(m.requestIdProvider.WithValue(r.Context(), entities.RequestId(requestId))))
}

func NewRequestIdMiddleware(config ports.Config, requestIdProvider providers.RequestIdProvider) middleware.Middleware {
	config.SetDefault("header", "X-Request-Id")

	return func(next http.Handler) http.Handler {
		return &RequestIdMiddleware{
			header:            config.GetString("header"),
			requestIdProvider: requestIdProvider,
			next:              next,
		}
	}
}

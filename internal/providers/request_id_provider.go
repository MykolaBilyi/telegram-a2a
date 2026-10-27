package providers

import (
	"telegram-a2a/internal/entities"
	"telegram-a2a/pkg/providers"
	"context"
)

//go:generate mockgen -destination mocks/request_id_provider.go -package provider_mocks . RequestIdProvider

type RequestIdProvider interface {
	Value(ctx context.Context) *entities.RequestId
	WithValue(ctx context.Context, value entities.RequestId) context.Context
}

func NewRequestIdProvider() RequestIdProvider {
	return providers.NewValueProvider[entities.RequestId]()
}

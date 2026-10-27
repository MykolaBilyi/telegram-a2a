package ports

import "time"

type Config interface {
	Sub(key string) Config
	GetString(key string) string
	GetStringSlice(key string) []string
	GetInt(key string) int
	GetInt64Slice(key string) []int64
	GetBool(key string) bool
	GetDuration(key string) time.Duration
	SetDefault(key string, value interface{})
}

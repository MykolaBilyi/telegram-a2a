package adapters

import (
	"strconv"
	"strings"
	"telegram-a2a/internal/ports"

	"github.com/spf13/viper"
)

type Config struct {
	*viper.Viper
}

var _ ports.Config = &Config{}

// NewConfig returns a new config instance.
func NewConfig(configPaths ...string) (*Config, error) {
	config := viper.New()
	config.SetConfigType("yml")
	config.SetConfigName("default")
	for _, p := range configPaths {
		config.AddConfigPath(p)
	}
	config.AddConfigPath("./configs/")
	config.AddConfigPath("../../configs/")
	config.AddConfigPath(".")

	config.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	config.AutomaticEnv()

	_ = config.BindEnv("telegram.botToken", "TELEGRAM_BOT_TOKEN")
	_ = config.BindEnv("telegram.allowedUsers", "TELEGRAM_ALLOWED_USERS")
	_ = config.BindEnv("telegram.apiURL", "TELEGRAM_API_URL")
	_ = config.BindEnv("telegram.pollTimeout", "TELEGRAM_POLL_TIMEOUT")
	_ = config.BindEnv("a2a.url", "KAGENT_A2A_URL", "A2A_URL")
	_ = config.BindEnv("a2a.timeout", "A2A_TIMEOUT")
	_ = config.BindEnv("logger.level", "LOG_LEVEL")

	if err := config.ReadInConfig(); err != nil {
		return nil, err
	}

	return &Config{config}, nil
}

func (c Config) Sub(key string) ports.Config {
	sub := c.Viper.Sub(key)
	if sub == nil {
		sub = viper.New()
	}
	sub.SetEnvPrefix(strings.ToUpper(strings.ReplaceAll(key, ".", "_")))
	sub.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	sub.AutomaticEnv()
	if key == "telegram" {
		_ = sub.BindEnv("botToken", "TELEGRAM_BOT_TOKEN")
		_ = sub.BindEnv("allowedUsers", "TELEGRAM_ALLOWED_USERS")
		_ = sub.BindEnv("apiURL", "TELEGRAM_API_URL")
		_ = sub.BindEnv("pollTimeout", "TELEGRAM_POLL_TIMEOUT")
	} else if key == "a2a" {
		_ = sub.BindEnv("url", "KAGENT_A2A_URL", "A2A_URL")
		_ = sub.BindEnv("timeout", "A2A_TIMEOUT")
	} else if key == "logger" {
		_ = sub.BindEnv("level", "LOG_LEVEL")
	}
	return &Config{sub}
}

func (c Config) GetInt64Slice(key string) []int64 {
	raw := c.Viper.Get(key)
	if raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case []int64:
		return v
	case []int:
		res := make([]int64, len(v))
		for i, val := range v {
			res[i] = int64(val)
		}
		return res
	case []any:
		res := make([]int64, 0, len(v))
		for _, item := range v {
			switch n := item.(type) {
			case int:
				res = append(res, int64(n))
			case int64:
				res = append(res, n)
			case float64:
				res = append(res, int64(n))
			case string:
				if parsed, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
					res = append(res, parsed)
				}
			}
		}
		return res
	case string:
		parts := strings.Split(v, ",")
		res := make([]int64, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if parsed, err := strconv.ParseInt(p, 10, 64); err == nil {
				res = append(res, parsed)
			}
		}
		return res
	}
	return nil
}

func NewTestConfig(configs ...map[string]any) ports.Config {
	v := viper.New()
	for _, c := range configs {
		err := v.MergeConfigMap(c)
		if err != nil {
			panic(err)
		}
	}
	return &Config{v}
}

package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type Config struct {
	Environment  string
	Port         string
	ValidAPIKeys []string

	// Redis
	RedisURL      string
	RedisPassword string
	RedisDB       int

	// pRPC
	PRPCEndpoint string
	PRPCSeedIPs  []string

	// Telegram Bot
	TelegramBotToken      string
	TelegramAdminPassword string
	GeminiAPIKey          string
	JupiterAPIKey         string

	// Firebase
	FirebaseProjectID   string
	FirebasePrivateKey  string
	FirebaseClientEmail string

	// Cache TTLs
	PNodeCacheTTL   time.Duration
	StatsCacheTTL   time.Duration
	HistoryCacheTTL time.Duration

	// Rate limiting
	RateLimitRPM int
}

func Load() *Config {
	// Prioritize the multi-key variable, but fall back to the single key for convenience
	validAPIKeysStr := getEnv("VALID_API_KEYS", "")
	if validAPIKeysStr == "" {
		// If multi-key is not set, try the single-key variable from the frontend .env
		singleKey := getEnv("API_KEY", "your-secret-api-key")
		validAPIKeysStr = singleKey
	}

	validAPIKeys := strings.Split(validAPIKeysStr, ",")
	// Trim spaces from each key
	for i, key := range validAPIKeys {
		validAPIKeys[i] = strings.TrimSpace(key)
	}

	cfg := &Config{
		Environment:  getEnv("ENVIRONMENT", "development"),
		Port:         getEnv("PORT", "8080"),
		ValidAPIKeys: validAPIKeys,

		RedisURL:      getEnv("REDIS_URL", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvAsInt("REDIS_DB", 0),

		PRPCEndpoint:          getEnv("PRPC_ENDPOINT", "https://xandeum.network"),
		PRPCSeedIPs:           []string{"173.212.220.65", "161.97.97.41", "192.190.136.36", "192.190.136.38", "207.244.255.1", "192.190.136.28", "192.190.136.29", "173.212.203.145"},
		TelegramBotToken:      getEnv("TELEGRAM_BOT_TOKEN", ""),
		TelegramAdminPassword: getEnv("TELEGRAM_ADMIN_PASSWORD", ""),
		GeminiAPIKey:          getEnv("GEMINI_API_KEY", ""),
		JupiterAPIKey:         getEnv("JUPITER_API_KEY", ""),

		FirebaseProjectID:   getEnv("FIREBASE_PROJECT_ID", ""),
		FirebasePrivateKey:  getEnv("FIREBASE_PRIVATE_KEY", ""),
		FirebaseClientEmail: getEnv("FIREBASE_CLIENT_EMAIL", ""),

		PNodeCacheTTL:   getEnvAsDuration("PNODE_CACHE_TTL", 30*time.Second),
		StatsCacheTTL:   getEnvAsDuration("STATS_CACHE_TTL", 5*time.Minute),
		HistoryCacheTTL: getEnvAsDuration("HISTORY_CACHE_TTL", time.Hour),

		RateLimitRPM: getEnvAsInt("RATE_LIMIT_RPM", 100),
	}

	// Validate required config
	if len(validAPIKeys) == 0 || (len(validAPIKeys) == 1 && validAPIKeys[0] == "") {
		logrus.Fatal("VALID_API_KEYS environment variable is required")
	}

	if cfg.RedisPassword == "" {
		logrus.Warn("REDIS_PASSWORD not set - Redis connection may fail")
	}

	if cfg.TelegramBotToken == "" {
		logrus.Info("TELEGRAM_BOT_TOKEN not set - Telegram bot will be disabled")
	}

	if cfg.GeminiAPIKey == "" {
		logrus.Info("GEMINI_API_KEY not set - AI features will be limited")
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

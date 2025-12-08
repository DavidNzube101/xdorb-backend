package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// APIKeyAuth middleware validates API key against list of valid keys
func APIKeyAuth(validKeys []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("Authorization")
		if apiKey == "" {
			// Try query parameter as fallback
			apiKey = c.Query("api_key")
		}

		// Remove "Bearer " prefix if present
		if len(apiKey) > 7 && apiKey[:7] == "Bearer " {
			apiKey = apiKey[7:]
		}

		// Check if API key is in the list of valid keys
		isValid := false
		for _, validKey := range validKeys {
			if apiKey == validKey {
				isValid = true
				break
			}
		}

		if !isValid {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid API key",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RateLimit implements token bucket rate limiting
func RateLimit(requestsPerMinute int) gin.HandlerFunc {
	// Simple in-memory rate limiter (for production, use Redis)
	type clientLimiter struct {
		tokens     int
		lastRefill time.Time
	}

	limiters := make(map[string]*clientLimiter)
	var mu sync.RWMutex

	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		mu.Lock()
		limiter, exists := limiters[clientIP]
		if !exists {
			limiter = &clientLimiter{
				tokens:     requestsPerMinute,
				lastRefill: time.Now(),
			}
			limiters[clientIP] = limiter
		}

		// Refill tokens based on time passed
		now := time.Now()
		timePassed := now.Sub(limiter.lastRefill)
		tokensToAdd := int(timePassed.Minutes() * float64(requestsPerMinute))
		if tokensToAdd > 0 {
			limiter.tokens = min(limiter.tokens+tokensToAdd, requestsPerMinute)
			limiter.lastRefill = now
		}

		// Check if request can proceed
		if limiter.tokens <= 0 {
			mu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "Rate limit exceeded",
			})
			c.Abort()
			return
		}

		limiter.tokens--
		mu.Unlock()

		c.Next()
	}
}

// CORS middleware
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// Logging middleware
func Logging() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		logrus.WithFields(logrus.Fields{
			"method":     param.Method,
			"path":       param.Path,
			"status":     param.StatusCode,
			"latency":    param.Latency,
			"client_ip":  param.ClientIP,
			"user_agent": param.Request.UserAgent(),
		}).Info("HTTP Request")

		return ""
	})
}

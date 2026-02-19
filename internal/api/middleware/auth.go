package middleware

import (
	"net/http"
	"strings"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/gin-gonic/gin"
)

// Auth API Key 认证中间件
func Auth(cfg *config.SecurityConfig) gin.HandlerFunc {
	// 构建 API Key 映射
	validKeys := make(map[string]string)
	for _, key := range cfg.APIKeys {
		validKeys[key.Key] = key.Name
	}

	return func(c *gin.Context) {
		// 从 Header 获取 API Key
		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			// 尝试从 Query 获取
			apiKey = c.Query("api_key")
		}

		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			c.Abort()
			return
		}

		// 验证 API Key
		if name, ok := validKeys[apiKey]; ok {
			c.Set("api_key_name", name)
			c.Next()
			return
		}

		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		c.Abort()
	}
}

// CORS 跨域中间件
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-API-Key")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// RequestLogger 请求日志中间件
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 跳过健康检查日志
		if strings.HasPrefix(c.Request.URL.Path, "/healthz") {
			c.Next()
			return
		}

		c.Next()

		// Gin 自带的日志已经足够
	}
}

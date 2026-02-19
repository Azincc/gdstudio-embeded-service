package api

import (
	"github.com/azin/gdstudio-embed-service/internal/api/handlers"
	"github.com/azin/gdstudio-embed-service/internal/api/middleware"
	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/gin-gonic/gin"
)

// SetupRouter 设置路由
func SetupRouter(cfg *config.Config, jobHandler *handlers.JobHandler) *gin.Engine {
	// 设置 Gin 模式
	gin.SetMode(cfg.Server.Mode)

	r := gin.Default()

	// 全局中间件
	r.Use(middleware.CORS())
	r.Use(middleware.RequestLogger())

	// 健康检查（无需认证）
	r.GET("/healthz", jobHandler.Health)
	r.GET("/readyz", jobHandler.Health)

	// API v1 路由组
	v1 := r.Group("/v1")
	v1.Use(middleware.Auth(&cfg.Security))
	{
		// 任务管理
		v1.POST("/jobs", jobHandler.Create)
		v1.GET("/jobs", jobHandler.List)
		v1.GET("/jobs/:id", jobHandler.Get)
		v1.POST("/jobs/:id/retry", jobHandler.Retry)
		v1.POST("/jobs/:id/cancel", jobHandler.Cancel)
	}

	return r
}

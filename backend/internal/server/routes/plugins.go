package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pluginruntime "github.com/Wei-Shaw/sub2api/internal/plugin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterPluginRoutes registers the fixed gateway used by hot-reloaded external plugins.
func RegisterPluginRoutes(
	r *gin.Engine,
	v1 *gin.RouterGroup,
	runtime *pluginruntime.Runtime,
	jwtAuth middleware.JWTAuthMiddleware,
	adminAuth middleware.AdminAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
	gameLedger *handler.GameLedgerHandler,
) {
	r.GET("/plugin-runtime/:id/ui/*path", runtime.ServeUI)
	r.HEAD("/plugin-runtime/:id/ui/*path", runtime.ServeUI)

	plugins := v1.Group("/plugins")
	plugins.Use(gin.HandlerFunc(jwtAuth))
	plugins.Use(middleware.BackendModeUserGuard(settingService))
	plugins.Use(panelRateLimiter.Global())
	{
		plugins.GET("", runtime.List)
		plugins.POST("/:id/invoke", runtime.Invoke)
	}

	admin := v1.Group("/admin/plugins")
	admin.Use(gin.HandlerFunc(adminAuth))
	admin.Use(panelRateLimiter.Global())
	admin.Use(gin.HandlerFunc(auditLog))
	admin.Use(middleware.AdminComplianceGuard(settingService))
	{
		admin.GET("", runtime.Diagnostics)
		admin.POST("/refresh", runtime.Refresh)
	}

	// Internal game ledger: called by game sidecars with their plugin
	// .api-secret. Never authenticated by user JWT or admin key.
	if gameLedger != nil {
		v1.POST("/internal/game/transactions", gameLedger.RecordTransaction)
		v1.POST("/internal/game/query", gameLedger.QueryBalance)
	}
}

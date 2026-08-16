package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type CatProxiesHandler struct {
	configs   *service.CatProxiesManagedProxyService
	runtime   *service.ManagedProxyRuntimeService
	targeting *service.CatProxiesTargetingService
}

func NewCatProxiesHandler(configs *service.CatProxiesManagedProxyService, runtime *service.ManagedProxyRuntimeService, targeting *service.CatProxiesTargetingService) *CatProxiesHandler {
	return &CatProxiesHandler{configs: configs, runtime: runtime, targeting: targeting}
}

type catProxiesConfigRequest struct {
	Name            string  `json:"name" binding:"required"`
	IsDefault       bool    `json:"is_default"`
	Protocol        string  `json:"protocol"`
	Host            string  `json:"host" binding:"required"`
	BaseUsername    string  `json:"base_username" binding:"required"`
	Password        *string `json:"password"`
	DefaultCountry  *string `json:"default_country"`
	DefaultState    *string `json:"default_state"`
	DefaultCity     *string `json:"default_city"`
	LifetimeMinutes int     `json:"lifetime_minutes"`
	Strict          *bool   `json:"strict"`
}

type catProxiesStatusRequest struct {
	Status string `json:"status" binding:"required"`
}
type managedProxyRequest struct {
	ProviderConfigID int64   `json:"provider_config_id" binding:"required"`
	Country          *string `json:"country"`
	State            *string `json:"state"`
	City             *string `json:"city"`
	Strict           *bool   `json:"strict"`
}
type managedProxyMigrateRequest struct {
	ProviderConfigID int64 `json:"provider_config_id" binding:"required"`
}

func (h *CatProxiesHandler) Targeting(c *gin.Context) {
	response.Success(c, h.targeting.Get(c.Query("country"), c.Query("state")))
}
func (h *CatProxiesHandler) ListConfigs(c *gin.Context) {
	value, err := h.configs.ListConfigs(c.Request.Context())
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) GetConfig(c *gin.Context) {
	id, ok := catProxiesID(c, "id")
	if !ok {
		return
	}
	value, err := h.configs.GetConfig(c.Request.Context(), id)
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) CreateConfig(c *gin.Context) {
	var req catProxiesConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	password := ""
	if req.Password != nil {
		password = *req.Password
	}
	value, err := h.configs.CreateConfig(c.Request.Context(), service.CreateCatProxyProviderConfigInput{Name: req.Name, IsDefault: req.IsDefault, Protocol: req.Protocol, Host: req.Host, BaseUsername: req.BaseUsername, Password: password, DefaultCountry: req.DefaultCountry, DefaultState: req.DefaultState, DefaultCity: req.DefaultCity, LifetimeMinutes: req.LifetimeMinutes, Strict: req.Strict})
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) UpdateConfig(c *gin.Context) {
	id, ok := catProxiesID(c, "id")
	if !ok {
		return
	}
	var req catProxiesConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	value, err := h.configs.UpdateConfig(c.Request.Context(), id, service.UpdateCatProxyProviderConfigInput{Name: req.Name, IsDefault: req.IsDefault, Protocol: req.Protocol, Host: req.Host, BaseUsername: req.BaseUsername, Password: req.Password, DefaultCountry: req.DefaultCountry, DefaultState: req.DefaultState, DefaultCity: req.DefaultCity, LifetimeMinutes: req.LifetimeMinutes, Strict: req.Strict})
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) UpdateConfigStatus(c *gin.Context) {
	id, ok := catProxiesID(c, "id")
	if !ok {
		return
	}
	var req catProxiesStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	value, err := h.configs.UpdateConfigStatus(c.Request.Context(), id, req.Status)
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) DeleteConfig(c *gin.Context) {
	id, ok := catProxiesID(c, "id")
	if !ok {
		return
	}
	if err := h.configs.DeleteConfig(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}
func (h *CatProxiesHandler) TestConfig(c *gin.Context) {
	id, ok := catProxiesID(c, "id")
	if !ok {
		return
	}
	value, err := h.configs.TestConfig(c.Request.Context(), id)
	catProxiesRespond(c, value, err)
}

func (h *CatProxiesHandler) ListManaged(c *gin.Context) {
	value, err := h.runtime.List(c.Request.Context())
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) GetManaged(c *gin.Context) {
	id, ok := catProxiesID(c, "account_id")
	if !ok {
		return
	}
	value, err := h.runtime.Get(c.Request.Context(), id)
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) Manage(c *gin.Context) {
	id, ok := catProxiesID(c, "account_id")
	if !ok {
		return
	}
	var req managedProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	value, err := h.runtime.Manage(c.Request.Context(), id, req.ProviderConfigID, service.CatProxiesProxyTarget{Country: req.Country, State: req.State, City: req.City, Strict: req.Strict})
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) Rotate(c *gin.Context) {
	id, ok := catProxiesID(c, "account_id")
	if !ok {
		return
	}
	value, err := h.runtime.Rotate(c.Request.Context(), id)
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) Migrate(c *gin.Context) {
	id, ok := catProxiesID(c, "account_id")
	if !ok {
		return
	}
	var req managedProxyMigrateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	value, err := h.runtime.Migrate(c.Request.Context(), id, req.ProviderConfigID)
	catProxiesRespond(c, value, err)
}
func (h *CatProxiesHandler) Release(c *gin.Context) {
	id, ok := catProxiesID(c, "account_id")
	if !ok {
		return
	}
	if err := h.runtime.Release(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}

func catProxiesID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}
func catProxiesRespond(c *gin.Context, value any, err error) {
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

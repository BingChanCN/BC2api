package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	pluginruntime "github.com/Wei-Shaw/sub2api/internal/plugin"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GameLedgerHandler exposes the internal game ledger endpoint consumed by
// game plugin sidecars. It is NOT a user endpoint: authentication is the
// plugin's shared .api-secret, never a user JWT or admin key.
type GameLedgerHandler struct {
	service  *service.GameLedgerService
	registry *pluginruntime.Registry
}

func NewGameLedgerHandler(svc *service.GameLedgerService) *GameLedgerHandler {
	return &GameLedgerHandler{service: svc}
}

// SetPluginRegistry binds the runtime plugin registry so the handler can
// authenticate sidecar bearer tokens and enforce manifest-declared caps.
func (h *GameLedgerHandler) SetPluginRegistry(registry *pluginruntime.Registry) {
	h.registry = registry
}

type gameTransactionBody struct {
	GameID  string  `json:"game_id" binding:"required"`
	Kind    string  `json:"kind" binding:"required"`
	RoundID string  `json:"round_id" binding:"required"`
	UserID  int64   `json:"user_id" binding:"required"`
	Amount  float64 `json:"amount" binding:"required"`
	Note    string  `json:"note"`
}

// RecordTransaction handles POST /api/v1/internal/game/transactions.
func (h *GameLedgerHandler) RecordTransaction(c *gin.Context) {
	if h.service == nil || h.registry == nil {
		response.Error(c, http.StatusServiceUnavailable, "game ledger is not available")
		return
	}

	var body gameTransactionBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	body.GameID = strings.TrimSpace(body.GameID)
	body.Kind = strings.TrimSpace(body.Kind)
	body.RoundID = strings.TrimSpace(body.RoundID)

	bearer := c.GetHeader("Authorization")
	policy, ok := h.registry.GamePolicy(body.GameID, bearer)
	if !ok {
		response.Error(c, http.StatusUnauthorized, "invalid or disabled game credential")
		return
	}

	req := service.GameTransactionRequest{
		GameID:  policy.ID,
		Kind:    body.Kind,
		RoundID: body.RoundID,
		UserID:  body.UserID,
		Amount:  body.Amount,
		Note:    strings.TrimSpace(body.Note),
	}
	if err := h.service.Validate(req, policy.MaxStake, policy.MaxPayout); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	result, err := h.service.RecordTransaction(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

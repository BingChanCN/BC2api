package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Game ledger error reasons (stable machine-readable codes).
const (
	GameErrReasonInvalidKind         = "GAME_INVALID_KIND"
	GameErrReasonRoundInProgress     = "GAME_ROUND_IN_PROGRESS"
	GameErrReasonRoundSettled        = "GAME_ROUND_SETTLED"
	GameErrReasonInsufficientBalance = "GAME_INSUFFICIENT_BALANCE"
	GameErrReasonAmountTooLarge      = "GAME_AMOUNT_TOO_LARGE"
)

// Game transaction idempotency status values stored in idempotency_records.
const (
	gameIdempotencyProcessing = "processing"
	gameIdempotencySucceeded  = "succeeded"
	gameIdempotencyFailed     = "failed"
)

var gameKindDirections = map[string]int{
	domain.GameKindStake:  -1, // 下注预扣
	domain.GameKindRefund: +1, // 退款返还
	domain.GameKindPayout: +1, // 奖金
	domain.GameKindBonus:  +1, // 活动奖励
}

// GameTransactionRequest is a single balance movement issued by a game sidecar.
type GameTransactionRequest struct {
	GameID  string
	Kind    string
	RoundID string
	UserID  int64
	Amount  float64
	Note    string
}

// GameTransactionResult is the outcome of a settled transaction.
type GameTransactionResult struct {
	Replayed     bool    `json:"replayed"`
	BalanceAfter float64 `json:"balance_after"`
}

// GameBalanceQuery is a read-only balance lookup issued by a game sidecar.
type GameBalanceQuery struct {
	GameID string
	UserID int64
}

// GameBalanceResult is the outcome of a balance query.
// Data is an object so future fields can be added without breaking
// the sidecar contract.
type GameBalanceResult struct {
	UserID   int64   `json:"user_id"`
	Balance  float64 `json:"balance"`
	Username string  `json:"username"`
}

// GameLedgerRepository persists idempotency bookkeeping for game transactions.
// All methods must work inside the caller's transaction context.
type GameLedgerRepository interface {
	InsertIdempotency(ctx context.Context, scope, keyHash, fingerprint string, expiresAt time.Time) (int64, bool, error)
	GetByIdempotencyKey(ctx context.Context, scope, keyHash string) (status, responseBody, errorReason string, err error)
	MarkSucceeded(ctx context.Context, id int64, responseBody string) error
	MarkFailed(ctx context.Context, id int64, errorReason string) error
}

// GameLedgerService is the single authority for game-driven balance movement.
// Sidecars never touch users.balance directly; every stake/payout/refund/bonus
// goes through RecordTransaction, which atomically updates the balance and
// writes a redeem_codes row with type=game for the audit trail.
type GameLedgerService struct {
	entClient            *dbent.Client
	userRepo             UserRepository
	redeemCodeRepo       RedeemCodeRepository
	ledgerRepo           GameLedgerRepository
	authCacheInvalidator APIKeyAuthCacheInvalidator
	billingCacheService  *BillingCacheService
}

func NewGameLedgerService(
	entClient *dbent.Client,
	userRepo UserRepository,
	redeemCodeRepo RedeemCodeRepository,
	ledgerRepo GameLedgerRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	billingCacheService *BillingCacheService,
) *GameLedgerService {
	return &GameLedgerService{
		entClient:            entClient,
		userRepo:             userRepo,
		redeemCodeRepo:       redeemCodeRepo,
		ledgerRepo:           ledgerRepo,
		authCacheInvalidator: authCacheInvalidator,
		billingCacheService:  billingCacheService,
	}
}

// QueryBalance returns the user's current balance without changing it.
// Sidecars call this for /me endpoints; the balance is read from the
// authoritative users table, not from any sidecar-side cache.
func (s *GameLedgerService) QueryBalance(ctx context.Context, q GameBalanceQuery) (*GameBalanceResult, error) {
	if q.UserID <= 0 {
		return nil, infraerrors.BadRequest("GAME_INVALID_USER", "user_id must be positive")
	}
	user, err := s.userRepo.GetByID(ctx, q.UserID)
	if err != nil {
		return nil, err
	}
	return &GameBalanceResult{UserID: q.UserID, Balance: user.Balance, Username: user.Username}, nil
}

// Validate checks the request shape and the plugin's declared caps.
// It is called by the handler after GamePolicy authentication.
func (s *GameLedgerService) Validate(req GameTransactionRequest, maxStake, maxPayout float64) error {
	if req.GameID == "" || req.RoundID == "" {
		return infraerrors.BadRequest("GAME_INVALID_ROUND", "game_id and round_id are required")
	}
	if len(req.RoundID) > 100 {
		return infraerrors.BadRequest("GAME_INVALID_ROUND", "round_id must not exceed 100 characters")
	}
	if req.UserID <= 0 {
		return infraerrors.BadRequest("GAME_INVALID_USER", "user_id must be positive")
	}
	if !(req.Amount > 0) {
		return infraerrors.BadRequest("GAME_INVALID_AMOUNT", "amount must be greater than 0")
	}
	if _, ok := gameKindDirections[req.Kind]; !ok {
		return infraerrors.BadRequest(GameErrReasonInvalidKind, "kind must be one of stake, payout, refund, bonus")
	}
	limit := maxPayout
	if req.Kind == domain.GameKindStake || req.Kind == domain.GameKindRefund {
		limit = maxStake
	}
	if req.Amount > limit {
		return infraerrors.Forbidden(GameErrReasonAmountTooLarge,
			fmt.Sprintf("amount %.4f exceeds the game limit %.4f for %s", req.Amount, limit, req.Kind))
	}
	if len(req.Note) > 300 {
		return infraerrors.BadRequest("GAME_INVALID_NOTE", "note must not exceed 300 characters")
	}
	return nil
}

// RecordTransaction executes one atomic, idempotent balance movement.
// The same (round_id, kind) can never settle twice: a replay returns the
// stored outcome without touching the balance.
func (s *GameLedgerService) RecordTransaction(ctx context.Context, req GameTransactionRequest) (*GameTransactionResult, error) {
	if _, ok := gameKindDirections[req.Kind]; !ok {
		return nil, infraerrors.BadRequest(GameErrReasonInvalidKind, "kind must be one of stake, payout, refund, bonus")
	}
	if !(req.Amount > 0) || req.UserID <= 0 || req.GameID == "" || req.RoundID == "" {
		return nil, infraerrors.BadRequest("GAME_INVALID_REQUEST", "invalid game transaction request")
	}

	scope := "game:" + req.GameID
	keyHash := sha256Hex(req.RoundID + ":" + req.Kind)
	fingerprint := sha256Hex(fmt.Sprintf("%d|%.8f|%s", req.UserID, req.Amount, req.Note))
	delta := float64(gameKindDirections[req.Kind]) * req.Amount

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, infraerrors.InternalServer("GAME_TX_START_FAILED", "failed to start game ledger transaction: "+err.Error())
	}
	defer func() { _ = tx.Rollback() }()
	opCtx := dbent.NewTxContext(ctx, tx)

	id, inserted, err := s.ledgerRepo.InsertIdempotency(opCtx, scope, keyHash, fingerprint, time.Now().Add(24*time.Hour))
	if err != nil {
		return nil, infraerrors.InternalServer("GAME_IDEMPOTENCY_FAILED", "failed to record game idempotency key: "+err.Error())
	}
	if !inserted {
		status, storedBody, storedReason, err := s.ledgerRepo.GetByIdempotencyKey(opCtx, scope, keyHash)
		if err != nil {
			return nil, infraerrors.InternalServer("GAME_IDEMPOTENCY_FAILED", "failed to read game idempotency record: "+err.Error())
		}
		switch status {
		case gameIdempotencySucceeded:
			var result GameTransactionResult
			if jsonErr := json.Unmarshal([]byte(storedBody), &result); jsonErr == nil {
				result.Replayed = true
				return &result, nil
			}
			return nil, infraerrors.Conflict(GameErrReasonRoundSettled, "round already settled")
		case gameIdempotencyFailed:
			return nil, infraerrors.Conflict(GameErrReasonRoundSettled, storedReason)
		default:
			return nil, infraerrors.Conflict(GameErrReasonRoundInProgress, "round transaction is already in progress")
		}
	}

	change, err := s.userRepo.AdjustBalance(opCtx, req.UserID, delta)
	if err != nil {
		reason := "failed to adjust balance"
		if errors.Is(err, ErrBalanceNegative) {
			reason = fmt.Sprintf("insufficient balance: current balance is %.4f", change.Old)
			_ = s.ledgerRepo.MarkFailed(opCtx, id, reason)
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, infraerrors.InternalServer("GAME_TX_COMMIT_FAILED", "failed to commit game ledger transaction: "+commitErr.Error())
			}
			return nil, infraerrors.Forbidden(GameErrReasonInsufficientBalance, reason)
		}
		return nil, infraerrors.InternalServer("GAME_BALANCE_FAILED", reason+": "+err.Error())
	}

	now := time.Now()
	code := "game-" + sha256Hex(req.GameID + ":" + req.RoundID + ":" + req.Kind)[:27]
	notes := fmt.Sprintf("%s%s:%s:%s %s", domain.GameLedgerNotesPrefix, req.GameID, req.Kind, req.RoundID, strings.TrimSpace(req.Note))
	if err := s.redeemCodeRepo.Create(opCtx, &RedeemCode{
		Code:   code,
		Type:   domain.AdjustmentTypeGame,
		Value:  change.New - change.Old,
		Status: StatusUsed,
		UsedBy: &req.UserID,
		UsedAt: &now,
		Notes:  notes,
	}); err != nil {
		return nil, infraerrors.InternalServer("GAME_RECORD_FAILED", "failed to write game balance record: "+err.Error())
	}

	result := GameTransactionResult{BalanceAfter: change.New}
	body, _ := json.Marshal(result)
	if err := s.ledgerRepo.MarkSucceeded(opCtx, id, string(body)); err != nil {
		return nil, infraerrors.InternalServer("GAME_IDEMPOTENCY_FAILED", "failed to finalize game idempotency record: "+err.Error())
	}
	if err := tx.Commit(); err != nil {
		return nil, infraerrors.InternalServer("GAME_TX_COMMIT_FAILED", "failed to commit game ledger transaction: "+err.Error())
	}

	// Balance changed: invalidate caches the same way admin adjustments do.
	s.invalidateCaches(ctx, req.UserID)

	return &result, nil
}

func (s *GameLedgerService) invalidateCaches(ctx context.Context, userID int64) {
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billingCacheService != nil {
		go func() {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.billingCacheService.InvalidateUserBalance(cacheCtx, userID); err != nil {
				logger.LegacyPrintf("service.game_ledger", "invalidate user balance cache failed: user_id=%d err=%v", userID, err)
			}
		}()
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

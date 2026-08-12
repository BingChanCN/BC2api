package service_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // test fixture constructs repository deps for the service constructor
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// regexpQueryMatcher matches expectations on SQL shape only, ignoring args.
// ent generates parameter-heavy INSERT statements whose exact arg list is
// noise for behavior tests.
func regexpQueryMatcher(expectedSQL, actualSQL string) error {
	if regexp.MustCompile(expectedSQL).MatchString(actualSQL) {
		return nil
	}
	return fmt.Errorf("SQL %q does not match %q", actualSQL, expectedSQL)
}

func newGameLedgerFixture(t *testing.T) (*service.GameLedgerService, sqlmock.Sqlmock, func()) {
	t.Helper()
	mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(regexpQueryMatcher)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mockDB.Close() })

	drv := entsql.OpenDB(dialect.Postgres, mockDB)
	client := dbent.NewClient(dbent.Driver(drv))
	t.Cleanup(func() { _ = client.Close() })

	userRepo := repository.NewUserRepository(client, mockDB)
	redeemRepo := repository.NewRedeemCodeRepository(client)
	ledgerRepo := repository.NewGameLedgerRepository(client)
	svc := service.NewGameLedgerService(client, userRepo, redeemRepo, ledgerRepo, nil, nil)
	return svc, mock, func() { require.NoError(t, mock.ExpectationsWereMet()) }
}

func TestGameLedgerValidate_CapsAndKinds(t *testing.T) {
	svc, _, _ := newGameLedgerFixture(t)

	base := service.GameTransactionRequest{GameID: "g", RoundID: "r1", UserID: 1, Amount: 5, Note: "x"}

	// 未知 kind
	bad := base
	bad.Kind = "cheat"
	err := svc.Validate(bad, 100, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stake, payout, refund, bonus")

	// stake 超上限
	bad = base
	bad.Kind = "stake"
	err = svc.Validate(bad, 4, 100)
	require.Error(t, err)
	require.Contains(t, err.Error(), "game limit")

	// payout 超上限
	bad = base
	bad.Kind = "payout"
	err = svc.Validate(bad, 100, 4)
	require.Error(t, err)

	// 正常
	good := base
	good.Kind = "stake"
	require.NoError(t, svc.Validate(good, 100, 100))
}

func TestGameLedgerQueryBalance_InvalidUserRejected(t *testing.T) {
	svc, _, check := newGameLedgerFixture(t)

	_, err := svc.QueryBalance(context.Background(), service.GameBalanceQuery{GameID: "g", UserID: 0})
	require.Error(t, err)
	require.Contains(t, err.Error(), "user_id must be positive")
	check()
}

func TestGameLedgerRecordTransaction_StakeSucceeds(t *testing.T) {
	svc, mock, check := newGameLedgerFixture(t)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO idempotency_records`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	// AdjustBalance(-1.5): 10.0 -> 8.5
	mock.ExpectQuery(`(?s)UPDATE users.*SET balance`).
		WillReturnRows(sqlmock.NewRows([]string{"old", "new"}).AddRow(10.0, 8.5))
	mock.ExpectQuery(`INSERT INTO "redeem_codes"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)UPDATE idempotency_records.*SET status = 'succeeded'`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	result, err := svc.RecordTransaction(context.Background(), service.GameTransactionRequest{
		GameID:  "reaction-grid",
		Kind:    "stake",
		RoundID: "round-1",
		UserID:  42,
		Amount:  1.5,
		Note:    "first bet",
	})
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.Equal(t, 8.5, result.BalanceAfter)
	check()
}

func TestGameLedgerRecordTransaction_ReplayReturnsStored(t *testing.T) {
	svc, mock, check := newGameLedgerFixture(t)

	// 第一次请求已结算；重放同 round+kind 时 INSERT 冲突，读回存储结果。
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO idempotency_records`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`(?s)SELECT status, COALESCE\(response_body`).
		WillReturnRows(sqlmock.NewRows([]string{"status", "response_body", "error_reason"}).
			AddRow("succeeded", `{"balance_after": 8.5}`, ""))
	mock.ExpectRollback()

	result, err := svc.RecordTransaction(context.Background(), service.GameTransactionRequest{
		GameID:  "reaction-grid",
		Kind:    "stake",
		RoundID: "round-1",
		UserID:  42,
		Amount:  1.5,
		Note:    "first bet",
	})
	require.NoError(t, err)
	require.True(t, result.Replayed)
	require.Equal(t, 8.5, result.BalanceAfter)
	check()
}

func TestGameLedgerRecordTransaction_InsufficientBalanceFailsPermanently(t *testing.T) {
	svc, mock, check := newGameLedgerFixture(t)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO idempotency_records`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	// AdjustBalance 匹配 0 行 → 查询当前余额 3.0，返回 ErrBalanceNegative
	mock.ExpectQuery(`(?s)UPDATE users.*SET balance`).
		WillReturnRows(sqlmock.NewRows([]string{"old", "new"}))
	mock.ExpectQuery(`SELECT balance FROM users`).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(3.0))
	mock.ExpectQuery(`(?s)UPDATE idempotency_records.*SET status = 'failed'`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	_, err := svc.RecordTransaction(context.Background(), service.GameTransactionRequest{
		GameID:  "reaction-grid",
		Kind:    "stake",
		RoundID: "round-1",
		UserID:  42,
		Amount:  5.0,
		Note:    "too expensive",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "insufficient balance")
	check()
}

func TestGameLedgerRecordTransaction_InvalidShapeRejectedWithoutDB(t *testing.T) {
	svc, _, check := newGameLedgerFixture(t)
	_, err := svc.RecordTransaction(context.Background(), service.GameTransactionRequest{
		GameID: "g", Kind: "stake", RoundID: "r1", UserID: 0, Amount: 1,
	})
	require.Error(t, err)
	check()
}

var _ = time.Now // keep time import for future expiry assertions

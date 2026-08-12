//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeRedeemRepoForUserBalance 实现 service.RedeemCodeRepository 最小子集
type fakeRedeemRepoForUserBalance struct {
	service.RedeemCodeRepository
	calls []int64 // 记录每次查询的 userID，验证只查当前用户
	items []service.RedeemCode
}

func (f *fakeRedeemRepoForUserBalance) ListByUserPaginated(_ context.Context, userID int64, _ pagination.PaginationParams, _ string) ([]service.RedeemCode, *pagination.PaginationResult, error) {
	f.calls = append(f.calls, userID)
	return f.items, &pagination.PaginationResult{Total: int64(len(f.items))}, nil
}

func TestGetMyBalanceHistory_UsesSubjectUserID(t *testing.T) {
	repo := &fakeRedeemRepoForUserBalance{items: []service.RedeemCode{
		{ID: 1, Type: "game", Value: -10},
		{ID: 2, Type: "game", Value: 19},
	}}
	h := NewUserBalanceHandler(repo)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/user/balance-history", nil)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
	h.GetMyBalanceHistory(c)

	require.Equal(t, 200, w.Code)
	// 权限边界：查询的 userID 必须来自 JWT subject，而不是请求参数
	require.Equal(t, []int64{42}, repo.calls)

	var body struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				ID    int64   `json:"id"`
				Type  string  `json:"type"`
				Value float64 `json:"value"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Len(t, body.Data.Items, 2)
	require.Equal(t, int64(2), body.Data.Total)
	require.Equal(t, "game", body.Data.Items[0].Type)
}

func TestGetMyBalanceHistory_Unauthenticated(t *testing.T) {
	repo := &fakeRedeemRepoForUserBalance{}
	h := NewUserBalanceHandler(repo)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/user/balance-history", nil)
	h.GetMyBalanceHistory(c)

	require.Equal(t, 401, w.Code)
	require.Empty(t, repo.calls, "未认证时不得触碰仓库")
}

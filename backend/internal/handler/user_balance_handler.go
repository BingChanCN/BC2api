package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UserBalanceHandler 用户侧余额明细：只读当前登录用户自己的流水。
type UserBalanceHandler struct {
	redeemCodeRepo service.RedeemCodeRepository
}

// NewUserBalanceHandler creates a new UserBalanceHandler.
func NewUserBalanceHandler(redeemCodeRepo service.RedeemCodeRepository) *UserBalanceHandler {
	return &UserBalanceHandler{redeemCodeRepo: redeemCodeRepo}
}

// GetMyBalanceHistory handles GET /user/balance-history
// 数据只来自当前 JWT 用户（used_by = subject.UserID），不接受任何目标用户参数，
// 与 admin 版（任意用户）在权限边界上刻意分离。
func (h *UserBalanceHandler) GetMyBalanceHistory(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	page, pageSize := response.ParsePagination(c)
	codeType := c.Query("type")

	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	codes, result, err := h.redeemCodeRepo.ListByUserPaginated(c.Request.Context(), subject.UserID, params, codeType)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.RedeemCode, 0, len(codes))
	for i := range codes {
		out = append(out, *dto.RedeemCodeFromService(&codes[i]))
	}

	total := int64(0)
	if result != nil {
		total = result.Total
	}
	pages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if pages < 1 {
		pages = 1
	}

	response.Success(c, gin.H{
		"items":     out,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"pages":     pages,
	})
}

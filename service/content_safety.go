package service

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func NewUpstreamContentBlockedError(c *gin.Context, adminReason string) *types.NewAPIError {
	if c != nil {
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, adminReason)
		common.SetContextKey(c, constant.ContextKeyUpstreamContentBlocked, true)
	}
	return types.NewOpenAIError(
		errors.New("upstream response blocked by content safety policy"),
		types.ErrorCodeEmptyResponse,
		http.StatusBadGateway,
	)
}

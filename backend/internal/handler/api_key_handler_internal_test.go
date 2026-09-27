//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 用户的「API 密钥」列表固定排除内部钥匙，其余筛选参数照常解析。
func TestUserAPIKeyListFiltersAlwaysExcludeInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/keys?search=abc&status=active&group_id=4", nil)

	filters := userAPIKeyListFilters(c)

	require.True(t, filters.ExcludeInternal)
	require.Equal(t, "abc", filters.Search)
	require.Equal(t, "active", filters.Status)
	require.NotNil(t, filters.GroupID)
	require.EqualValues(t, 4, *filters.GroupID)
}

func TestRejectReservedAPIKeyName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	require.True(t, rejectReservedAPIKeyName(c, service.PlaygroundKeyName))
	require.Equal(t, http.StatusBadRequest, w.Code)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	require.False(t, rejectReservedAPIKeyName(c, "我的钥匙"))
	require.False(t, c.Writer.Written(), "普通名字不能写任何响应")
}

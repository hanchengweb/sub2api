//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakePlaygroundKeyStore struct {
	keys         []service.APIKey
	balance      float64
	balanceErr   error
	balanceReads int
	created      []service.CreateAPIKeyRequest
}

func (f *fakePlaygroundKeyStore) List(context.Context, int64, pagination.PaginationParams, service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return f.keys, nil, nil
}

func (f *fakePlaygroundKeyStore) Create(_ context.Context, userID int64, req service.CreateAPIKeyRequest) (*service.APIKey, error) {
	f.created = append(f.created, req)
	return &service.APIKey{ID: 100, UserID: userID, Key: "sk-new", Name: req.Name, Status: service.StatusActive, Quota: req.Quota}, nil
}

func (f *fakePlaygroundKeyStore) UserBalance(context.Context, int64) (float64, error) {
	f.balanceReads++
	return f.balance, f.balanceErr
}

func playgroundKeyTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat", nil)
	return c
}

// 有可用钥匙就直接用：不查余额、不建新钥匙。这是绝大多数请求走的路。
func TestResolveUserKeyUsesExistingKey(t *testing.T) {
	store := &fakePlaygroundKeyStore{keys: []service.APIKey{{ID: 1, Key: "sk-mine", Status: service.StatusActive}}}
	h := &PlaygroundHandler{apiKeyService: store}

	key, err := h.resolveUserKey(playgroundKeyTestContext(), 7)

	require.NoError(t, err)
	require.Equal(t, "sk-mine", key.Key)
	require.Zero(t, store.balanceReads)
	require.Empty(t, store.created)
}

// 新用户（注册不送积分）打开在线使用：不给他建钥匙，直接引导去充值。
func TestResolveUserKeyZeroBalanceNeedsTopUp(t *testing.T) {
	for name, keys := range map[string][]service.APIKey{
		"一把都没有":    nil,
		"老试用密钥已用满": {{ID: 1, Key: "sk-old", Status: service.StatusActive, Quota: 1000, QuotaUsed: 1000}},
		"只有停用的钥匙":  {{ID: 2, Key: "sk-off", Status: "disabled"}},
	} {
		store := &fakePlaygroundKeyStore{keys: keys, balance: 0}
		h := &PlaygroundHandler{apiKeyService: store}

		_, err := h.resolveUserKey(playgroundKeyTestContext(), 7)

		require.ErrorIs(t, err, errPlaygroundNeedsTopUp, name)
		require.Empty(t, store.created, "%s：余额为 0 不能给用户建钥匙", name)
	}
}

// 充了值但没建过钥匙：建一把不限额的「在线使用」，花的是他自己的余额。
func TestResolveUserKeyCreatesPlaygroundKeyWhenFunded(t *testing.T) {
	store := &fakePlaygroundKeyStore{balance: 500}
	h := &PlaygroundHandler{apiKeyService: store}

	key, err := h.resolveUserKey(playgroundKeyTestContext(), 7)

	require.NoError(t, err)
	require.Equal(t, "sk-new", key.Key)
	require.Len(t, store.created, 1)
	require.Equal(t, service.PlaygroundKeyName, store.created[0].Name)
	require.Zero(t, store.created[0].Quota, "不能再带一个假的额度上限")
}

// 查余额失败不能当成「余额为 0」去提示充值——那会误导已经付过钱的用户。
func TestResolveUserKeyBalanceErrorIsNotTopUp(t *testing.T) {
	store := &fakePlaygroundKeyStore{balanceErr: errors.New("db down")}
	h := &PlaygroundHandler{apiKeyService: store}

	_, err := h.resolveUserKey(playgroundKeyTestContext(), 7)

	require.Error(t, err)
	require.NotErrorIs(t, err, errPlaygroundNeedsTopUp)
	require.Empty(t, store.created)
}

// 余额不足回的码必须和网关余额不足一致，前端才能用同一套「去充值」引导接住。
func TestWritePlaygroundKeyErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{errPlaygroundNeedsTopUp, "INSUFFICIENT_BALANCE"},
		{errors.New("create failed"), "NO_USABLE_API_KEY"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		writePlaygroundKeyError(c, tc.err)

		require.Equal(t, http.StatusForbidden, w.Code)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, tc.code, body.Error.Code)
	}
}

// 构造时传入空指针不能变成「非 nil 的接口」，否则一调方法就 panic。
func TestNewPlaygroundHandlerWithoutKeyService(t *testing.T) {
	h := NewPlaygroundHandler(nil, nil, nil, nil, nil)
	require.Nil(t, h.apiKeyService)

	_, err := h.resolveUserKey(playgroundKeyTestContext(), 7)
	require.Error(t, err)
}

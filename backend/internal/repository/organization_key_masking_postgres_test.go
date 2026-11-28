package repository_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// 用真实迁移、仓储、管理服务和管理接口验证脱敏：属主的账号类型必须能从真实查询里取到。
// 组织服务账号的 key 不出现在任何一个管理接口响应里，个人账号的返回不变。
func TestAdminAPIKeyResponsesMaskOrganizationKeysPostgres(t *testing.T) {
	dsn := os.Getenv("MEDIA_QUOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("MEDIA_QUOTA_TEST_DSN not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		u.Path != "/media_quota_test" || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatal("MEDIA_QUOTA_TEST_DSN must target media_quota_test on a literal loopback address")
	}
	for _, key := range []string{"host", "hostaddr", "port", "dbname", "service"} {
		require.False(t, u.Query().Has(key), "test DSN must not override %s", key)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	name := fmt.Sprintf("key_masking_%d", time.Now().UnixNano())
	_, err = root.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name)+" TEMPLATE template0")
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, dropErr := root.ExecContext(cleanupCtx, "DROP DATABASE "+pq.QuoteIdentifier(name)+" WITH (FORCE)")
		require.NoError(t, dropErr)
	})
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))

	group, err := client.Group.Create().SetName("masking-group").Save(ctx)
	require.NoError(t, err)
	organization, err := client.User.Create().SetEmail("masking-org@service.invalid").SetPasswordHash("x").
		SetAccountType(service.AccountTypeOrganizationService).SetOrganizationIssuer("wemoreai-ops").
		SetOrganizationID("masking-organization").SetOrganizationEnvironment("test").Save(ctx)
	require.NoError(t, err)
	person, err := client.User.Create().SetEmail("masking-person@example.test").SetPasswordHash("x").Save(ctx)
	require.NoError(t, err)
	const orgKey, personalKey = "orgkey0123456789abcdefghijklWXYZ", "sk-personal0123456789abcdefghijklmnQRST"
	orgKeyRow, err := client.APIKey.Create().SetUserID(organization.ID).SetGroupID(group.ID).SetKey(orgKey).SetName("org").Save(ctx)
	require.NoError(t, err)
	personalKeyRow, err := client.APIKey.Create().SetUserID(person.ID).SetGroupID(group.ID).SetKey(personalKey).SetName("person").Save(ctx)
	require.NoError(t, err)
	account, err := client.Account.Create().SetName("masking-account").SetPlatform("openai").SetType("apikey").Save(ctx)
	require.NoError(t, err)
	for _, row := range []*dbent.APIKey{orgKeyRow, personalKeyRow} {
		_, err = client.UsageLog.Create().SetUserID(row.UserID).SetAPIKeyID(row.ID).SetAccountID(account.ID).
			SetRequestID(fmt.Sprintf("masking-%d", row.ID)).SetModel("synthetic").Save(ctx)
		require.NoError(t, err)
	}

	users := repository.NewUserRepository(client, db)
	keys := repository.NewAPIKeyRepository(client, db)
	adminService := service.NewAdminService(users, nil, nil, nil, keys, nil, nil, nil, nil, nil, nil, nil, client,
		nil, nil, nil, nil, nil, nil, nil, nil)
	usage := service.NewUsageService(repository.NewUsageLogRepository(client, db), nil, nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	userHandler := admin.NewUserHandler(adminService, nil, nil, nil, nil, nil, nil)
	router.GET("/admin/users/:id", userHandler.GetByID)
	router.GET("/admin/users/:id/api-keys", userHandler.GetUserAPIKeys)
	router.GET("/admin/groups/:id/api-keys", admin.NewGroupHandler(adminService, nil, nil).GetGroupAPIKeys)
	router.PUT("/admin/api-keys/:id", admin.NewAdminAPIKeyHandler(adminService, nil).UpdateGroup)
	router.GET("/admin/usage", admin.NewUsageHandler(usage, nil, nil, nil).List)

	call := func(method, path, body string) string {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "%s %s: %s", method, path, w.Body.String())
		return w.Body.String()
	}
	const orgMasked = `"key":"orgkey...WXYZ"`
	personalPlain := `"key":"` + personalKey + `"`

	require.NotContains(t, call(http.MethodGet, fmt.Sprintf("/admin/users/%d", organization.ID), ""), orgKey)

	body := call(http.MethodGet, fmt.Sprintf("/admin/users/%d/api-keys", organization.ID), "")
	require.NotContains(t, body, orgKey)
	require.Contains(t, body, orgMasked)
	require.Contains(t, call(http.MethodGet, fmt.Sprintf("/admin/users/%d/api-keys", person.ID), ""), personalPlain)

	body = call(http.MethodGet, fmt.Sprintf("/admin/groups/%d/api-keys", group.ID), "")
	require.NotContains(t, body, orgKey)
	require.Contains(t, body, orgMasked)
	require.Contains(t, body, personalPlain)

	body = call(http.MethodPut, fmt.Sprintf("/admin/api-keys/%d", orgKeyRow.ID), `{}`)
	require.NotContains(t, body, orgKey)
	require.Contains(t, body, orgMasked)
	require.Contains(t, call(http.MethodPut, fmt.Sprintf("/admin/api-keys/%d", personalKeyRow.ID), `{}`), personalPlain)

	body = call(http.MethodGet, "/admin/usage?exact_total=true", "")
	require.NotContains(t, body, orgKey)
	require.Contains(t, body, orgMasked)
	require.Contains(t, body, personalPlain)

	// 脱敏只作用于响应，库里的 key 保持原值，鉴权不受影响。
	stored, err := keys.GetByKeyForAuth(ctx, orgKey)
	require.NoError(t, err)
	require.Equal(t, orgKeyRow.ID, stored.ID)
}

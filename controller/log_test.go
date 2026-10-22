package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupLogQueryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dialect := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_LOG_QUERY_DIALECT")))
	if dialect == "" {
		dialect = "sqlite"
	}
	databaseTypes := map[string]common.DatabaseType{
		"sqlite":   common.DatabaseTypeSQLite,
		"mysql":    common.DatabaseTypeMySQL,
		"postgres": common.DatabaseTypePostgreSQL,
	}
	databaseType, ok := databaseTypes[dialect]
	require.True(t, ok, "unsupported TEST_LOG_QUERY_DIALECT %q", dialect)

	previousLogDB := model.LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	database, _ := newAuditTestDatabase(t, dialect, os.Getenv("TEST_"+strings.ToUpper(dialect)+"_DSN"))
	require.NoError(t, database.AutoMigrate(&model.Log{}))
	model.LOG_DB = database
	common.SetDatabaseTypes(databaseType, databaseType)
	t.Cleanup(func() {
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})

	versionQuery := "SELECT version()"
	if dialect == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, database.Raw(versionQuery).Scan(&version).Error)
	t.Logf("log query database: %s %s", dialect, version)
	return database
}

func TestGetLogByKeyFiltersByRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousLogDB := model.LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Log{}))
	model.LOG_DB = database
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.LOG_DB = previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&[]model.Log{
		{Id: 1, TokenId: 11, RequestId: "request-a", UpstreamRequestId: "upstream-a", Type: model.LogTypeConsume},
		{Id: 2, TokenId: 11, RequestId: "request-b", UpstreamRequestId: "upstream-b", Type: model.LogTypeConsume},
		{Id: 3, TokenId: 12, RequestId: "request-a", Type: model.LogTypeConsume},
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/log/token?request_id=request-b", nil)
	ctx.Set("token_id", 11)

	GetLogByKey(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool        `json:"success"`
		Data    []model.Log `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data, 1)
	assert.Equal(t, "request-b", response.Data[0].RequestId)
}

func TestGetLogsFiltersByMinQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := setupLogQueryTestDB(t)
	require.NoError(t, database.Create(&[]model.Log{
		{Id: 1, UserId: 7, CreatedAt: 100, Type: model.LogTypeConsume, Quota: 50},
		{Id: 2, UserId: 7, CreatedAt: 200, Type: model.LogTypeConsume, Quota: 100},
		{Id: 3, UserId: 7, CreatedAt: 300, Type: model.LogTypeConsume, Quota: 101},
		{Id: 4, UserId: 7, CreatedAt: 400, Type: model.LogTypeConsume, Quota: 200},
		{Id: 5, UserId: 7, CreatedAt: 500, Type: model.LogTypeTopup, Quota: 1000},
	}).Error)

	endpoints := []struct {
		name       string
		handler    gin.HandlerFunc
		setContext func(*gin.Context)
	}{
		{
			name:    "admin",
			handler: GetAllLogs,
			setContext: func(c *gin.Context) {
				c.Set("role", common.RoleRootUser)
			},
		},
		{
			name:    "user",
			handler: GetUserLogs,
			setContext: func(c *gin.Context) {
				c.Set("id", 7)
			},
		},
	}
	testCases := []struct {
		name        string
		query       string
		wantSuccess bool
		wantQuota   []int
	}{
		{"at or above threshold", "&min_quota=100", true, []int{200, 101, 100}},
		{"missing threshold", "", true, []int{200, 101, 100, 50}},
		{"empty threshold", "&min_quota=", true, []int{200, 101, 100, 50}},
		{"zero threshold", "&min_quota=0", true, []int{200, 101, 100, 50}},
		{"invalid threshold", "&min_quota=invalid", false, nil},
		{"overflow threshold", "&min_quota=999999999999999999999999999", false, nil},
		{"negative threshold", "&min_quota=-1", false, nil},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			for _, testCase := range testCases {
				t.Run(testCase.name, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(recorder)
					ctx.Request = httptest.NewRequest(http.MethodGet, "/api/log?type=2"+testCase.query, nil)
					endpoint.setContext(ctx)

					endpoint.handler(ctx)

					require.Equal(t, http.StatusOK, recorder.Code)
					var response struct {
						Success bool `json:"success"`
						Data    struct {
							Total int         `json:"total"`
							Items []model.Log `json:"items"`
						} `json:"data"`
					}
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
					require.Equal(t, testCase.wantSuccess, response.Success)
					if !testCase.wantSuccess {
						return
					}
					require.Equal(t, len(testCase.wantQuota), response.Data.Total)
					quotas := make([]int, len(response.Data.Items))
					for i := range response.Data.Items {
						quotas[i] = response.Data.Items[i].Quota
					}
					assert.Equal(t, testCase.wantQuota, quotas)
				})
			}
		})
	}
}

func TestGetLogsStatFiltersByMinQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := setupLogQueryTestDB(t)
	now := time.Now().Unix()
	require.NoError(t, database.Create(&[]model.Log{
		{Id: 1, UserId: 7, Username: "alice", CreatedAt: now - 50, Type: model.LogTypeConsume, Quota: 50, PromptTokens: 10, CompletionTokens: 5},
		{Id: 2, UserId: 7, Username: "alice", CreatedAt: now - 40, Type: model.LogTypeConsume, Quota: 100, PromptTokens: 20, CompletionTokens: 10},
		{Id: 3, UserId: 7, Username: "alice", CreatedAt: now - 30, Type: model.LogTypeConsume, Quota: 200, PromptTokens: 30, CompletionTokens: 15},
		{Id: 4, UserId: 8, Username: "bob", CreatedAt: now - 20, Type: model.LogTypeConsume, Quota: 200, PromptTokens: 40, CompletionTokens: 20},
		{Id: 5, UserId: 7, Username: "alice", CreatedAt: now - 10, Type: model.LogTypeTopup, Quota: 1000},
	}).Error)

	testCases := []struct {
		name       string
		handler    gin.HandlerFunc
		setContext func(*gin.Context)
		wantQuota  int
		wantRPM    int
		wantTPM    int
	}{
		{
			name:    "admin",
			handler: GetLogsStat,
			setContext: func(c *gin.Context) {
				c.Set("role", common.RoleRootUser)
			},
			wantQuota: 500,
			wantRPM:   3,
			wantTPM:   135,
		},
		{
			name:    "self",
			handler: GetLogsSelfStat,
			setContext: func(c *gin.Context) {
				c.Set("username", "alice")
			},
			wantQuota: 300,
			wantRPM:   2,
			wantTPM:   75,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/log/stat?type=2&min_quota=100", nil)
			testCase.setContext(ctx)

			testCase.handler(ctx)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					Quota int `json:"quota"`
					RPM   int `json:"rpm"`
					TPM   int `json:"tpm"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success)
			assert.Equal(t, testCase.wantQuota, response.Data.Quota)
			assert.Equal(t, testCase.wantRPM, response.Data.RPM)
			assert.Equal(t, testCase.wantTPM, response.Data.TPM)
		})
	}
}

package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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

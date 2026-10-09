package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withHeaderNavModules(t *testing.T, raw string) {
	t.Helper()

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, hadPrevious := common.OptionMap["HeaderNavModules"]
	common.OptionMap["HeaderNavModules"] = raw
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if hadPrevious {
			common.OptionMap["HeaderNavModules"] = previous
			return
		}
		delete(common.OptionMap, "HeaderNavModules")
	})
}

func performHeaderNavRequest(t *testing.T, handler gin.HandlerFunc, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()

	if !authenticated {
		return performHeaderNavRequestWithGroup(t, handler, "")
	}
	return performHeaderNavRequestWithGroup(t, handler, "default")
}

// performHeaderNavRequestWithGroup authenticates as a user of the given account
// group; an empty group sends an anonymous request. Extra handlers are
// registered between the module gate and the test handler, mirroring the route
// wiring that adds HeaderNavGroupAuth behind HeaderNavModuleAuth.
func performHeaderNavRequestWithGroup(t *testing.T, handler gin.HandlerFunc, group string, extra ...gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := append([]gin.HandlerFunc{handler}, extra...)
	handlers = append(handlers, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	router.GET("/api/test", handlers...)

	var accessToken string
	if group != "" {
		previousDB, previousRedis := model.DB, common.RedisEnabled
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}))
		model.DB = db
		common.RedisEnabled = false
		require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
		t.Cleanup(func() {
			model.DB = previousDB
			common.RedisEnabled = previousRedis
		})
		accessToken = "header-nav-pat"
		user := model.User{
			Username:    "tester",
			Password:    "unused-password-hash",
			Role:        common.RoleCommonUser,
			Status:      common.UserStatusEnabled,
			Group:       group,
			AuthVersion: 1,
		}
		user.SetAccessToken(accessToken)
		require.NoError(t, db.Create(&user).Error)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if group != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestHeaderNavModuleAuthAllowsDefaultPublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("pricing"), false)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHeaderNavModuleAuthRejectsDisabledPricing(t *testing.T) {
	raw := `{"pricing":{"enabled":false,"requireAuth":false}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("pricing"), false)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestHeaderNavModuleAuthRequiresLoginForPricing(t *testing.T) {
	raw := `{"pricing":{"enabled":true,"requireAuth":true}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModuleAuthRequiresLoginForRankings(t *testing.T) {
	raw := `{"rankings":{"enabled":true,"requireAuth":true}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("rankings"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModuleAuthRejectsLegacyDisabledModule(t *testing.T) {
	raw := `{"rankings":false}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModuleAuth("rankings"), false)

	require.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthAllowsDefaultPublicAccess(t *testing.T) {
	withHeaderNavModules(t, "")

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthRequiresLoginWhenDisabled(t *testing.T) {
	raw := `{"pricing":{"enabled":false,"requireAuth":false}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthAllowsLoggedInWhenDisabled(t *testing.T) {
	raw := `{"pricing":{"enabled":false,"requireAuth":false}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), true)

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthRequiresLoginWhenRequireAuth(t *testing.T) {
	raw := `{"pricing":{"enabled":true,"requireAuth":true}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavModulePublicOrUserAuthRequiresLoginForLegacyDisabledModule(t *testing.T) {
	raw := `{"pricing":false}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequest(t, HeaderNavModulePublicOrUserAuth("pricing"), false)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestHeaderNavPublicRouteRejectsExpiredInternalAccessToken(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	withHeaderNavModules(t, "")
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/api/test", HeaderNavModuleAuth("pricing"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	request := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	request.Header.Set("Authorization", "Bearer "+issueExpiredDashboardAccessToken(t, service.AuthIdentity{
		UserID: 1, SessionID: "expired-header-nav-session", UserAuthVersion: 1, SessionVersion: 1,
	}))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Contains(t, response.Body.String(), "AUTH_TOKEN_EXPIRED")
}

func TestHeaderNavModuleAuthEnforcesVisibleGroups(t *testing.T) {
	raw := `{"pricing":{"enabled":true,"requireAuth":false,"groups":["公司内部","vip"]}}`
	withHeaderNavModules(t, raw)

	allowed := performHeaderNavRequestWithGroup(t, HeaderNavModuleAuth("pricing"), "公司内部", HeaderNavGroupAuth("pricing"))
	require.Equal(t, http.StatusOK, allowed.Code)

	denied := performHeaderNavRequestWithGroup(t, HeaderNavModuleAuth("pricing"), "default", HeaderNavGroupAuth("pricing"))
	require.Equal(t, http.StatusForbidden, denied.Code)

	anonymous := performHeaderNavRequestWithGroup(t, HeaderNavModuleAuth("pricing"), "", HeaderNavGroupAuth("pricing"))
	require.Equal(t, http.StatusUnauthorized, anonymous.Code)
}

func TestHeaderNavModuleAuthAcceptsCommaSeparatedVisibleGroups(t *testing.T) {
	raw := `{"rankings":{"enabled":true,"groups":"vip, 公司内部"}}`
	withHeaderNavModules(t, raw)

	recorder := performHeaderNavRequestWithGroup(t, HeaderNavModuleAuth("rankings"), "vip", HeaderNavGroupAuth("rankings"))

	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestHeaderNavModuleAuthWithoutGroupsStaysPublic(t *testing.T) {
	raw := `{"pricing":{"enabled":true,"requireAuth":false},"rankings":{"enabled":true,"groups":[]}}`
	withHeaderNavModules(t, raw)

	require.Equal(t, http.StatusOK, performHeaderNavRequestWithGroup(t, HeaderNavModuleAuth("pricing"), "").Code)
	require.Equal(t, http.StatusOK, performHeaderNavRequestWithGroup(t, HeaderNavModuleAuth("rankings"), "").Code)
}

func TestVisibleHeaderNavModulesHidesRestrictedModules(t *testing.T) {
	raw := `{"home":false,"pricing":{"enabled":true,"requireAuth":true,"groups":["vip"]},"rankings":{"enabled":true,"requireAuth":false}}`
	withHeaderNavModules(t, raw)

	moduleEnabled := func(t *testing.T, group string) map[string]bool {
		t.Helper()
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		if group != "" {
			c.Set("group", group)
		}
		var parsed map[string]any
		require.NoError(t, common.UnmarshalJsonStr(VisibleHeaderNavModules(c), &parsed))
		result := make(map[string]bool, len(parsed))
		for module, value := range parsed {
			access, ok := value.(map[string]any)
			if !ok {
				continue
			}
			enabled, ok := access["enabled"].(bool)
			require.True(t, ok)
			result[module] = enabled
		}
		return result
	}

	anonymous := moduleEnabled(t, "")
	require.False(t, anonymous["pricing"])
	require.True(t, anonymous["rankings"])

	allowed := moduleEnabled(t, "vip")
	require.True(t, allowed["pricing"])

	denied := moduleEnabled(t, "default")
	require.False(t, denied["pricing"])
	require.True(t, denied["rankings"])
}

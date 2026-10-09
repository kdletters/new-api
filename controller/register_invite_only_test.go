package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const registerInviteTestCode = "INV1"

// setupRegisterInviteOnlyTest isolates the user table and the registration
// switches for one invite-only registration case.
func setupRegisterInviteOnlyTest(t *testing.T) {
	t.Helper()
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis := common.RedisEnabled
	previousRegister, previousPasswordRegister := common.RegisterEnabled, common.PasswordRegisterEnabled
	previousInviteOnly := common.InviteOnlyRegistrationEnabled
	previousEmailVerification := common.EmailVerificationEnabled
	db, _ := newAuditTestDatabase(t, "sqlite", "")
	require.NoError(t, db.AutoMigrate(&model.User{}))
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.InviteOnlyRegistrationEnabled = false
	common.EmailVerificationEnabled = false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled = previousRedis
		common.RegisterEnabled = previousRegister
		common.PasswordRegisterEnabled = previousPasswordRegister
		common.InviteOnlyRegistrationEnabled = previousInviteOnly
		common.EmailVerificationEnabled = previousEmailVerification
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
}

func performRegister(t *testing.T, body string) (bool, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Register(c)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload.Success, payload.Message
}

func TestRegisterInviteOnlyRegistration(t *testing.T) {
	tests := []struct {
		name            string
		inviteOnly      bool
		affCode         string
		withInviter     bool
		inviterStatus   int
		wantSuccess     bool
		wantMessageKey  string
		wantInviterLink bool
	}{
		{name: "open registration ignores missing invite code", wantSuccess: true},
		{name: "invite-only requires an invite code", inviteOnly: true, wantMessageKey: i18n.MsgUserInviteCodeRequired},
		{name: "invite-only rejects unknown invite code", inviteOnly: true, affCode: "UNKN", wantMessageKey: i18n.MsgUserInviteCodeInvalid},
		{name: "invite-only rejects disabled inviter", inviteOnly: true, affCode: registerInviteTestCode, withInviter: true, inviterStatus: common.UserStatusDisabled, wantMessageKey: i18n.MsgUserInviteCodeInvalid},
		{name: "invite-only accepts active inviter", inviteOnly: true, affCode: registerInviteTestCode, withInviter: true, inviterStatus: common.UserStatusEnabled, wantSuccess: true, wantInviterLink: true},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setupRegisterInviteOnlyTest(t)
			common.InviteOnlyRegistrationEnabled = test.inviteOnly
			inviterId := 0
			if test.withInviter {
				inviter := model.User{Username: "inviter", Password: "hashed-password", Role: common.RoleCommonUser, Status: test.inviterStatus, Group: "default", AffCode: registerInviteTestCode}
				require.NoError(t, model.DB.Create(&inviter).Error)
				inviterId = inviter.Id
			}

			username := fmt.Sprintf("invite-user-%d", index)
			body := fmt.Sprintf(`{"username":%q,"password":"registration-password","aff_code":%q}`, username, test.affCode)
			success, message := performRegister(t, body)
			assert.Equal(t, test.wantSuccess, success, message)
			if test.wantMessageKey != "" {
				assert.Equal(t, i18n.Translate(i18n.DefaultLang, test.wantMessageKey), message)
			}

			var created model.User
			err := model.DB.Where("username = ?", username).First(&created).Error
			if !test.wantSuccess {
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				return
			}
			require.NoError(t, err)
			if test.wantInviterLink {
				assert.Equal(t, inviterId, created.InviterId)
			}
		})
	}
}

func TestOAuthInviteOnlyRegistrationDoesNotCreateUser(t *testing.T) {
	setupAuthFlowControllerTest(t)
	previousInviteOnly := common.InviteOnlyRegistrationEnabled
	common.InviteOnlyRegistrationEnabled = true
	t.Cleanup(func() { common.InviteOnlyRegistrationEnabled = previousInviteOnly })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/oauth/login", nil)

	_, _, err := findOrCreateOAuthUser(c, &authFlowTestOAuthProvider{}, &oauth.OAuthUser{ProviderUserID: "invite-only-user"}, nil, "")
	require.Error(t, err)
	var inviteOnlyErr *OAuthInviteOnlyRegistrationError
	require.ErrorAs(t, err, &inviteOnlyErr)

	var count int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&count).Error)
	assert.Zero(t, count, "invite-only registration must not auto-create OAuth users")
}

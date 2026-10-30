package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func loadTokenQuotaResetTestToken(t *testing.T, id int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, id).Error)
	return token
}

func TestQuotaResetScheduleMatchesSubscription(t *testing.T) {
	base := time.Date(2026, 10, 30, 18, 45, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	for _, tc := range []struct {
		name      string
		period    string
		customSec int64
	}{
		{name: "daily", period: SubscriptionResetDaily},
		{name: "weekly", period: SubscriptionResetWeekly},
		{name: "monthly", period: SubscriptionResetMonthly},
		{name: "custom", period: SubscriptionResetCustom, customSec: 3600},
		{name: "never", period: SubscriptionResetNever},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := &SubscriptionPlan{QuotaResetPeriod: tc.period, QuotaResetCustomSeconds: tc.customSec}
			assert.Equal(t,
				calcNextResetTime(base, plan, 0),
				calcQuotaResetNextTime(base, tc.period, tc.customSec, 0),
			)
		})
	}
	assert.Equal(t,
		base.Unix()+MaxQuotaResetCustomSeconds+1,
		calcQuotaResetNextTime(base, SubscriptionResetCustom, MaxQuotaResetCustomSeconds+1, 0),
	)
}

func TestAdvanceTokenQuotaResetScheduleDoesNotIterateMissedPeriods(t *testing.T) {
	now := GetDBTimestamp()
	longAgo := now - 50*365*24*3600
	for _, tc := range []struct {
		name     string
		token    Token
		wantLast func(time.Time) int64
	}{
		{
			name: "custom one second",
			token: Token{
				QuotaResetPeriod: SubscriptionResetCustom, QuotaResetCustomSeconds: 1,
				LastResetTime: longAgo, NextResetTime: longAgo + 1,
			},
			wantLast: func(time.Time) int64 { return now },
		},
		{
			name: "daily",
			token: Token{
				QuotaResetPeriod: SubscriptionResetDaily,
				LastResetTime:    longAgo, NextResetTime: longAgo + 24*3600,
			},
			wantLast: func(nowTime time.Time) int64 {
				return time.Date(nowTime.Year(), nowTime.Month(), nowTime.Day(), 0, 0, 0, 0, nowTime.Location()).Unix()
			},
		},
		{
			name: "weekly",
			token: Token{
				QuotaResetPeriod: SubscriptionResetWeekly,
				LastResetTime:    longAgo, NextResetTime: longAgo + 7*24*3600,
			},
			wantLast: func(nowTime time.Time) int64 {
				weekday := int(nowTime.Weekday())
				if weekday == 0 {
					weekday = 7
				}
				return time.Date(nowTime.Year(), nowTime.Month(), nowTime.Day(), 0, 0, 0, 0, nowTime.Location()).
					AddDate(0, 0, 1-weekday).Unix()
			},
		},
		{
			name: "monthly",
			token: Token{
				QuotaResetPeriod: SubscriptionResetMonthly,
				LastResetTime:    longAgo, NextResetTime: longAgo + 31*24*3600,
			},
			wantLast: func(nowTime time.Time) int64 {
				return time.Date(nowTime.Year(), nowTime.Month(), 1, 0, 0, 0, 0, nowTime.Location()).Unix()
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			advanced, lastReset, nextReset := advanceTokenQuotaResetSchedule(&tc.token, now)
			assert.True(t, advanced)
			assert.Equal(t, tc.wantLast(time.Unix(now, 0)), lastReset)
			assert.Greater(t, nextReset, now)
		})
	}
}

func TestRecalculateTokenQuotaResetNormalizesCustomSeconds(t *testing.T) {
	now := GetDBTimestamp()
	token := Token{
		QuotaResetPeriod: SubscriptionResetDaily, QuotaResetCustomSeconds: 3600,
		LastResetTime: 1, NextResetTime: 1,
	}
	RecalculateTokenQuotaReset(&token, now)
	assert.Zero(t, token.QuotaResetCustomSeconds)
	assert.Equal(t, now, token.LastResetTime)
	assert.Greater(t, token.NextResetTime, now)

	token = Token{QuotaResetPeriod: SubscriptionResetNever, QuotaResetCustomSeconds: 3600}
	RecalculateTokenQuotaReset(&token, now)
	assert.Zero(t, token.QuotaResetCustomSeconds)
	assert.Zero(t, token.LastResetTime)
	assert.Zero(t, token.NextResetTime)
}

func TestResetDueTokensPreservesAllocationAdvancesScheduleAndRecoversStatus(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	dailyLastReset := now - 2*24*3600
	customLastReset := now - 25

	dueExhausted := &Token{
		UserId: 1, Key: "reset-due-exhausted", Name: "due-exhausted", Status: common.TokenStatusExhausted,
		ExpiredTime: -1, RemainQuota: 20, UsedQuota: 80, QuotaResetPeriod: SubscriptionResetDaily,
		LastResetTime: dailyLastReset, NextResetTime: now - 1,
	}
	notDue := &Token{
		UserId: 1, Key: "reset-not-due", Name: "not-due", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 60, UsedQuota: 40, QuotaResetPeriod: SubscriptionResetDaily,
		LastResetTime: now - 3600, NextResetTime: now + 3600,
	}
	customDisabled := &Token{
		UserId: 1, Key: "reset-custom-disabled", Name: "custom-disabled", Status: common.TokenStatusDisabled,
		ExpiredTime: -1, RemainQuota: 5, UsedQuota: 15, QuotaResetPeriod: SubscriptionResetCustom,
		QuotaResetCustomSeconds: 10, LastResetTime: customLastReset, NextResetTime: now - 1,
	}
	never := &Token{
		UserId: 1, Key: "reset-never", Name: "never", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: 7, UsedQuota: 3, QuotaResetPeriod: SubscriptionResetNever,
		NextResetTime: now - 1,
	}
	expiredExhausted := &Token{
		UserId: 1, Key: "reset-expired-exhausted", Name: "expired-exhausted", Status: common.TokenStatusExhausted,
		ExpiredTime: now - 1, RemainQuota: 0, UsedQuota: 50, QuotaResetPeriod: SubscriptionResetDaily,
		LastResetTime: dailyLastReset, NextResetTime: now - 1,
	}
	zeroExhausted := &Token{
		UserId: 1, Key: "reset-zero-exhausted", Name: "zero-exhausted", Status: common.TokenStatusExhausted,
		ExpiredTime: -1, RemainQuota: 0, UsedQuota: 0, QuotaResetPeriod: SubscriptionResetDaily,
		LastResetTime: dailyLastReset, NextResetTime: now - 1,
	}
	for _, token := range []*Token{dueExhausted, notDue, customDisabled, never, expiredExhausted, zeroExhausted} {
		require.NoError(t, DB.Create(token).Error)
	}

	resetCount, err := ResetDueTokens(100)
	require.NoError(t, err)
	assert.Equal(t, 4, resetCount)

	token := loadTokenQuotaResetTestToken(t, dueExhausted.Id)
	assert.Equal(t, 100, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, common.TokenStatusEnabled, token.Status)
	assert.Greater(t, token.NextResetTime, now)
	assert.Greater(t, token.LastResetTime, dailyLastReset)

	token = loadTokenQuotaResetTestToken(t, notDue.Id)
	assert.Equal(t, 60, token.RemainQuota)
	assert.Equal(t, 40, token.UsedQuota)
	assert.Equal(t, now+3600, token.NextResetTime)

	token = loadTokenQuotaResetTestToken(t, customDisabled.Id)
	assert.Equal(t, 20, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	assert.Equal(t, now-5, token.LastResetTime)
	assert.Equal(t, now+5, token.NextResetTime)

	token = loadTokenQuotaResetTestToken(t, never.Id)
	assert.Equal(t, 7, token.RemainQuota)
	assert.Equal(t, 3, token.UsedQuota)
	assert.Zero(t, token.NextResetTime)

	token = loadTokenQuotaResetTestToken(t, expiredExhausted.Id)
	assert.Equal(t, 50, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	assert.Equal(t, common.TokenStatusExhausted, token.Status)

	token = loadTokenQuotaResetTestToken(t, zeroExhausted.Id)
	assert.Zero(t, token.RemainQuota)
	assert.Equal(t, common.TokenStatusExhausted, token.Status)

	firstResetToken := loadTokenQuotaResetTestToken(t, dueExhausted.Id)
	resetCount, err = ResetDueTokens(100)
	require.NoError(t, err)
	assert.Zero(t, resetCount, "a second run must be idempotent")
	afterSecondRun := loadTokenQuotaResetTestToken(t, dueExhausted.Id)
	assert.Equal(t, firstResetToken.RemainQuota, afterSecondRun.RemainQuota)
	assert.Equal(t, firstResetToken.UsedQuota, afterSecondRun.UsedQuota)
	assert.Equal(t, firstResetToken.Status, afterSecondRun.Status)
	assert.Equal(t, firstResetToken.LastResetTime, afterSecondRun.LastResetTime)
	assert.Equal(t, firstResetToken.NextResetTime, afterSecondRun.NextResetTime)
}

func TestUpdateUserTokenWithLockPreservesFreshQuotaResetState(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	token := &Token{
		UserId: 1, Key: "locked-update-reset-state", Name: "before", Status: common.TokenStatusExhausted,
		ExpiredTime: -1, RemainQuota: 20, UsedQuota: 80, QuotaResetPeriod: SubscriptionResetCustom,
		QuotaResetCustomSeconds: 3600, LastResetTime: now - 3600, NextResetTime: now - 1,
	}
	require.NoError(t, DB.Create(token).Error)

	resetCount, err := ResetDueTokens(10)
	require.NoError(t, err)
	require.Equal(t, 1, resetCount)

	updated, err := UpdateUserTokenWithLock(token.UserId, token.Id, func(current *Token) error {
		assert.Equal(t, 100, current.RemainQuota)
		assert.Zero(t, current.UsedQuota)
		assert.Equal(t, common.TokenStatusEnabled, current.Status)
		current.Name = "after"
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "after", updated.Name)
	assert.Equal(t, 100, updated.RemainQuota)
	assert.Zero(t, updated.UsedQuota)
	assert.Equal(t, common.TokenStatusEnabled, updated.Status)
	assert.Greater(t, updated.NextResetTime, now)
}

func TestResetDueTokensWaitsForBatchUpdateGrace(t *testing.T) {
	truncateTables(t)
	previousBatchEnabled := common.BatchUpdateEnabled
	previousBatchInterval := common.BatchUpdateInterval
	common.BatchUpdateEnabled = true
	common.BatchUpdateInterval = 5
	t.Cleanup(func() {
		common.BatchUpdateEnabled = previousBatchEnabled
		common.BatchUpdateInterval = previousBatchInterval
	})

	now := GetDBTimestamp()
	token := &Token{
		UserId: 1, Key: "batch-grace-token", Name: "batch-grace", Status: common.TokenStatusExhausted,
		ExpiredTime: -1, RemainQuota: 0, UsedQuota: 100, QuotaResetPeriod: SubscriptionResetDaily,
		LastResetTime: now - 24*3600, NextResetTime: now - 1,
	}
	require.NoError(t, DB.Create(token).Error)

	resetCount, err := ResetDueTokens(10)
	require.NoError(t, err)
	assert.Zero(t, resetCount, "fresh batch deltas must have time to flush before reset")

	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).
		Update("next_reset_time", now-20).Error)
	resetCount, err = ResetDueTokens(10)
	require.NoError(t, err)
	assert.Equal(t, 1, resetCount)
}

func TestResetDueTokensInvalidatesTokenCache(t *testing.T) {
	truncateTables(t)
	_ = useUserCacheMiniRedis(t)
	now := GetDBTimestamp()
	token := &Token{
		UserId: 1, Key: "reset-cache-invalidation", Name: "cache-invalidation", Status: common.TokenStatusExhausted,
		ExpiredTime: -1, RemainQuota: 25, UsedQuota: 75, QuotaResetPeriod: SubscriptionResetDaily,
		LastResetTime: now - 2*24*3600, NextResetTime: now - 1,
	}
	require.NoError(t, DB.Create(token).Error)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	_, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)

	resetCount, err := ResetDueTokens(100)
	require.NoError(t, err)
	assert.Equal(t, 1, resetCount)
	_, err = cacheGetTokenByKey(token.Key)
	assert.Error(t, err)

	fresh, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, fresh.RemainQuota)
	assert.Zero(t, fresh.UsedQuota)
	assert.Equal(t, common.TokenStatusEnabled, fresh.Status)
}

type legacyQuotaResetToken struct {
	Id                 int    `gorm:"primaryKey"`
	UserId             int    `gorm:"index"`
	Key                string `gorm:"type:varchar(128);uniqueIndex"`
	Status             int    `gorm:"default:1"`
	Name               string `gorm:"index"`
	CreatedTime        int64  `gorm:"bigint"`
	AccessedTime       int64  `gorm:"bigint"`
	ExpiredTime        int64  `gorm:"bigint;default:-1"`
	RemainQuota        int    `gorm:"default:0"`
	UnlimitedQuota     bool
	ModelLimitsEnabled bool
	ModelLimits        string  `gorm:"type:text"`
	AllowIps           *string `gorm:"default:''"`
	UsedQuota          int     `gorm:"default:0"`
	Group              string  `gorm:"column:group;default:''"`
	CrossGroupRetry    bool
	AutoGroups         string         `gorm:"type:text"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

func (legacyQuotaResetToken) TableName(namer schema.Namer) string {
	return namer.TableName("Token")
}

func TestTokenQuotaResetMigrationCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         string
		database    common.DatabaseType
		openDialect func(string) gorm.Dialector
	}{
		{name: "sqlite", database: common.DatabaseTypeSQLite, openDialect: func(string) gorm.Dialector {
			return sqlite.Open(filepath.Join(t.TempDir(), "token_quota_reset.db"))
		}},
		{name: "mysql", env: "TEST_MYSQL_DSN", database: common.DatabaseTypeMySQL, openDialect: mysql.Open},
		{name: "postgres", env: "TEST_POSTGRES_DSN", database: common.DatabaseTypePostgreSQL, openDialect: func(dsn string) gorm.Dialector {
			return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := os.Getenv(tc.env)
			if tc.env != "" && strings.TrimSpace(dsn) == "" {
				t.Skip(tc.env + " is not configured")
			}
			prefix := fmt.Sprintf("token_rst_%s_%d_", tc.name, time.Now().UnixNano())
			tableName := prefix + "tokens"
			db, err := gorm.Open(tc.openDialect(dsn), &gorm.Config{
				NamingStrategy: schema.NamingStrategy{TablePrefix: prefix},
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)

			previousDB := DB
			previousMainType := common.MainDatabaseType()
			previousLogType := common.LogDatabaseType()
			DB = db
			common.SetDatabaseTypes(tc.database, tc.database)
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(tableName))
				DB = previousDB
				common.SetDatabaseTypes(previousMainType, previousLogType)
				require.NoError(t, sqlDB.Close())
			})

			versionQuery := "SELECT version()"
			if tc.database == common.DatabaseTypeSQLite {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			require.NoError(t, db.AutoMigrate(&legacyQuotaResetToken{}))
			legacy := legacyQuotaResetToken{
				UserId: 9, Key: "legacy-token-" + prefix, Status: common.TokenStatusEnabled,
				Name: "legacy-token", CreatedTime: 1, AccessedTime: 2, ExpiredTime: -1,
				RemainQuota: 321, UsedQuota: 17, Group: "default", AutoGroups: "",
				ModelLimits: "gpt-4o", ModelLimitsEnabled: true,
			}
			require.NoError(t, db.Create(&legacy).Error)

			require.NoError(t, db.AutoMigrate(&Token{}))
			require.NoError(t, db.AutoMigrate(&Token{}))
			for _, column := range []string{"quota_reset_period", "quota_reset_custom_seconds", "last_reset_time", "next_reset_time"} {
				assert.True(t, db.Migrator().HasColumn(&Token{}, column), "missing column %s", column)
			}
			indexName := db.Config.NamingStrategy.IndexName(tableName, "next_reset_time")
			assert.True(t, db.Migrator().HasIndex(&Token{}, indexName), "missing index %s", indexName)

			var migrated Token
			require.NoError(t, db.First(&migrated, legacy.Id).Error)
			assert.Equal(t, legacy.Key, migrated.Key)
			assert.Equal(t, legacy.RemainQuota, migrated.RemainQuota)
			assert.Equal(t, legacy.UsedQuota, migrated.UsedQuota)
			assert.Equal(t, SubscriptionResetNever, migrated.QuotaResetPeriod)
			assert.Zero(t, migrated.QuotaResetCustomSeconds)
			assert.Zero(t, migrated.LastResetTime)
			assert.Zero(t, migrated.NextResetTime)

			inserted := Token{
				UserId: 10, Key: "new-token-" + prefix, Name: "new-token", Status: common.TokenStatusEnabled,
				ExpiredTime: -1, RemainQuota: 123,
			}
			require.NoError(t, db.Create(&inserted).Error)
			var reloaded Token
			require.NoError(t, db.First(&reloaded, inserted.Id).Error)
			assert.Equal(t, SubscriptionResetNever, reloaded.QuotaResetPeriod)
			assert.Zero(t, reloaded.QuotaResetCustomSeconds)

			t.Run("fresh schema", func(t *testing.T) {
				require.NoError(t, db.Migrator().DropTable(tableName))
				require.NoError(t, db.AutoMigrate(&Token{}))
				require.NoError(t, db.AutoMigrate(&Token{}))
				for _, column := range []string{"quota_reset_period", "quota_reset_custom_seconds", "last_reset_time", "next_reset_time"} {
					assert.True(t, db.Migrator().HasColumn(&Token{}, column), "missing fresh-schema column %s", column)
				}
				assert.True(t, db.Migrator().HasIndex(&Token{}, indexName), "missing fresh-schema index %s", indexName)

				freshToken := Token{
					UserId: 11, Key: "fresh-token-" + prefix, Name: "fresh-token", Status: common.TokenStatusEnabled,
					ExpiredTime: -1, RemainQuota: 456,
				}
				require.NoError(t, db.Create(&freshToken).Error)
				var loaded Token
				require.NoError(t, db.First(&loaded, freshToken.Id).Error)
				assert.Equal(t, SubscriptionResetNever, loaded.QuotaResetPeriod)
				assert.Zero(t, loaded.QuotaResetCustomSeconds)
				assert.Zero(t, loaded.LastResetTime)
				assert.Zero(t, loaded.NextResetTime)
			})
		})
	}
}

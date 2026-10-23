package model

import (
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

func seedSubscriptionResetPlan(t *testing.T, plan *SubscriptionPlan) {
	t.Helper()
	require.NoError(t, DB.Create(plan).Error)
}

func seedSubscriptionResetSub(t *testing.T, sub *UserSubscription) {
	t.Helper()
	require.NoError(t, DB.Create(sub).Error)
}

func getSubscriptionResetSub(t *testing.T, id int) UserSubscription {
	t.Helper()
	var sub UserSubscription
	require.NoError(t, DB.Where("id = ?", id).First(&sub).Error)
	return sub
}

func TestAdminResetUserSubscriptionsByPlanResetsAllActiveMatchesAndAdvancesTime(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9101,
		Title:            "Pro",
		PriceAmount:      10,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      1000,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	otherPlan := &SubscriptionPlan{
		Id:               9102,
		Title:            "Basic",
		PriceAmount:      1,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetPlan(t, otherPlan)

	activeEnd := now + 30*24*3600
	expiredEnd := now - 1
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9201, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 300, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9202, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 500, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9203, UserId: 101, PlanId: otherPlan.Id, AmountTotal: 100, AmountUsed: 60, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9204, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 700, StartTime: now - 7200, EndTime: expiredEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now - 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9205, UserId: 102, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 800, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 120})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9206, UserId: 101, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 900, StartTime: now - 3600, EndTime: activeEnd, Status: "cancelled", LastResetTime: now - 3600, NextResetTime: now + 120})

	beforeReset := GetDBTimestamp()
	result, err := AdminResetUserSubscriptionsByPlan(101, plan.Id, true)
	afterReset := GetDBTimestamp()

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, plan.Id, result.PlanId)
	assert.Equal(t, 2, result.MatchedCount)
	assert.Equal(t, 2, result.ResetCount)
	assert.Equal(t, 1, result.UserCount)
	assert.Equal(t, []int{101}, result.AffectedUserIds)
	assert.True(t, result.AdvanceResetTime)

	for _, id := range []int{9201, 9202} {
		sub := getSubscriptionResetSub(t, id)
		assert.Zero(t, sub.AmountUsed)
		assert.GreaterOrEqual(t, sub.LastResetTime, beforeReset)
		assert.LessOrEqual(t, sub.LastResetTime, afterReset)
		assert.Equal(t, calcNextResetTime(time.Unix(sub.LastResetTime, 0), plan, sub.EndTime), sub.NextResetTime)
	}
	assert.EqualValues(t, 60, getSubscriptionResetSub(t, 9203).AmountUsed)
	assert.EqualValues(t, 700, getSubscriptionResetSub(t, 9204).AmountUsed)
	assert.EqualValues(t, 800, getSubscriptionResetSub(t, 9205).AmountUsed)
	assert.EqualValues(t, 900, getSubscriptionResetSub(t, 9206).AmountUsed)
}

func TestAdminResetUserSubscriptionsByPlanKeepsResetTimes(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9301,
		Title:            "Team",
		PriceAmount:      20,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      2000,
		QuotaResetPeriod: SubscriptionResetMonthly,
	}
	seedSubscriptionResetPlan(t, plan)

	lastReset := now - 86400
	nextReset := now + 86400
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9302, UserId: 201, PlanId: plan.Id, AmountTotal: 2000, AmountUsed: 1200, StartTime: now - 172800, EndTime: now + 30*24*3600, Status: "active", LastResetTime: lastReset, NextResetTime: nextReset})

	result, err := AdminResetUserSubscriptionsByPlan(201, plan.Id, false)

	require.NoError(t, err)
	assert.False(t, result.AdvanceResetTime)
	sub := getSubscriptionResetSub(t, 9302)
	assert.Zero(t, sub.AmountUsed)
	assert.Equal(t, lastReset, sub.LastResetTime)
	assert.Equal(t, nextReset, sub.NextResetTime)
}

func TestAdminResetUserSubscriptionsByPlanNoActiveMatchReturnsError(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:            9401,
		Title:         "Expired",
		PriceAmount:   10,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9402, UserId: 301, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 500, StartTime: now - 7200, EndTime: now - 1, Status: "active"})

	result, err := AdminResetUserSubscriptionsByPlan(301, plan.Id, true)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, strings.Contains(err.Error(), "This user has no active subscription to this plan"))
}

func TestAdminResetPlanSubscriptionsResetsAllActiveUsers(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9501,
		Title:            "Business",
		PriceAmount:      30,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      3000,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	seedSubscriptionResetPlan(t, plan)

	activeEnd := now + 30*24*3600
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9502, UserId: 401, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1000, StartTime: now - 3600, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9503, UserId: 401, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1100, StartTime: now - 3500, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9504, UserId: 402, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1200, StartTime: now - 3400, EndTime: activeEnd, Status: "active", LastResetTime: now - 3600, NextResetTime: now + 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9505, UserId: 403, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1300, StartTime: now - 7200, EndTime: now - 1, Status: "active", LastResetTime: now - 3600, NextResetTime: now - 10})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9506, UserId: 404, PlanId: plan.Id, AmountTotal: 3000, AmountUsed: 1400, StartTime: now - 3600, EndTime: activeEnd, Status: "cancelled", LastResetTime: now - 3600, NextResetTime: now + 10})

	result, err := AdminResetPlanSubscriptions(plan.Id, true)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.MatchedCount)
	assert.Equal(t, 3, result.ResetCount)
	assert.Equal(t, 2, result.UserCount)
	assert.Equal(t, []int{401, 402}, result.AffectedUserIds)
	for _, id := range []int{9502, 9503, 9504} {
		sub := getSubscriptionResetSub(t, id)
		assert.Zero(t, sub.AmountUsed)
		assert.Zero(t, sub.LastResetTime)
		assert.Zero(t, sub.NextResetTime)
	}
	assert.EqualValues(t, 1300, getSubscriptionResetSub(t, 9505).AmountUsed)
	assert.EqualValues(t, 1400, getSubscriptionResetSub(t, 9506).AmountUsed)
}

func TestAdminResetPlanSubscriptionsNoMatchSucceeds(t *testing.T) {
	truncateTables(t)

	plan := &SubscriptionPlan{
		Id:            9601,
		Title:         "Empty",
		PriceAmount:   10,
		DurationUnit:  SubscriptionDurationMonth,
		DurationValue: 1,
		TotalAmount:   1000,
	}
	seedSubscriptionResetPlan(t, plan)

	result, err := AdminResetPlanSubscriptions(plan.Id, true)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Zero(t, result.MatchedCount)
	assert.Zero(t, result.ResetCount)
	assert.Zero(t, result.UserCount)
	assert.Empty(t, result.AffectedUserIds)
}

func TestSyncPlanSubscriptionsQuotaSnapshotUpdatesActiveSubscriptions(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9701,
		Title:            "Pro",
		PriceAmount:      10,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      1000,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	otherPlan := &SubscriptionPlan{
		Id:               9702,
		Title:            "Basic",
		PriceAmount:      1,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      100,
		QuotaResetPeriod: SubscriptionResetDaily,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetPlan(t, otherPlan)

	activeEnd := now + 30*24*3600
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9711, UserId: 301, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 300, StartTime: now - 3600, EndTime: activeEnd, Status: "active"})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9712, UserId: 302, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 0, StartTime: now - 3600, EndTime: activeEnd, Status: "active"})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9713, UserId: 301, PlanId: otherPlan.Id, AmountTotal: 100, AmountUsed: 10, StartTime: now - 3600, EndTime: activeEnd, Status: "active"})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9714, UserId: 303, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 0, StartTime: now - 7200, EndTime: now - 1, Status: "active"})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9715, UserId: 304, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 0, StartTime: now - 3600, EndTime: activeEnd, Status: "cancelled"})

	updated := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		updated, err = SyncPlanSubscriptionsQuotaSnapshotTx(tx, plan.Id, 500000, now)
		return err
	})

	require.NoError(t, err)
	assert.Equal(t, 2, updated)
	first := getSubscriptionResetSub(t, 9711)
	assert.EqualValues(t, 500000, first.AmountTotal)
	assert.EqualValues(t, 300, first.AmountUsed)
	assert.EqualValues(t, 500000, getSubscriptionResetSub(t, 9712).AmountTotal)
	assert.EqualValues(t, 100, getSubscriptionResetSub(t, 9713).AmountTotal)
	assert.EqualValues(t, 1000, getSubscriptionResetSub(t, 9714).AmountTotal)
	assert.EqualValues(t, 1000, getSubscriptionResetSub(t, 9715).AmountTotal)
}

func TestSyncPlanSubscriptionsQuotaSnapshotRejectsUsedExceedingNewTotal(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9721,
		Title:            "Pro",
		PriceAmount:      10,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      1000,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	seedSubscriptionResetPlan(t, plan)

	activeEnd := now + 30*24*3600
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9731, UserId: 401, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 600, StartTime: now - 3600, EndTime: activeEnd, Status: "active"})
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9732, UserId: 402, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 100, StartTime: now - 3600, EndTime: activeEnd, Status: "active"})

	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := SyncPlanSubscriptionsQuotaSnapshotTx(tx, plan.Id, 500, now)
		return err
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "已用额度超过")
	assert.EqualValues(t, 1000, getSubscriptionResetSub(t, 9731).AmountTotal)
	assert.EqualValues(t, 1000, getSubscriptionResetSub(t, 9732).AmountTotal)
}

func TestSyncPlanSubscriptionsQuotaSnapshotAllowsUnlimitedAndValidatesBounds(t *testing.T) {
	truncateTables(t)

	now := GetDBTimestamp()
	plan := &SubscriptionPlan{
		Id:               9741,
		Title:            "Pro",
		PriceAmount:      10,
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		TotalAmount:      1000,
		QuotaResetPeriod: SubscriptionResetNever,
	}
	seedSubscriptionResetPlan(t, plan)
	seedSubscriptionResetSub(t, &UserSubscription{Id: 9751, UserId: 501, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 700, StartTime: now - 3600, EndTime: now + 30*24*3600, Status: "active"})

	updated := 0
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		updated, err = SyncPlanSubscriptionsQuotaSnapshotTx(tx, plan.Id, 0, now)
		return err
	}))
	assert.Equal(t, 1, updated)
	sub := getSubscriptionResetSub(t, 9751)
	assert.Zero(t, sub.AmountTotal)
	assert.EqualValues(t, 700, sub.AmountUsed)

	_, err := SyncPlanSubscriptionsQuotaSnapshotTx(DB, plan.Id, -1, now)
	require.Error(t, err)
	_, err = SyncPlanSubscriptionsQuotaSnapshotTx(DB, plan.Id, common.MaxWalletQuota+1, now)
	require.Error(t, err)
	_, err = SyncPlanSubscriptionsQuotaSnapshotTx(DB, 0, 100, now)
	require.Error(t, err)
}

func TestSyncPlanSubscriptionsQuotaSnapshotDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			var databaseType common.DatabaseType
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "subscription_snapshot.db"))
				databaseType = common.DatabaseTypeSQLite
			case "mysql":
				dsn := strings.TrimSpace(os.Getenv("TEST_MYSQL_DSN"))
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
				databaseType = common.DatabaseTypeMySQL
			case "postgres":
				dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
				databaseType = common.DatabaseTypePostgreSQL
			}

			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "sub_snapshot_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)

			previousDB := DB
			previousMainType := common.MainDatabaseType()
			previousLogType := common.LogDatabaseType()
			DB = db
			common.SetDatabaseTypes(databaseType, databaseType)
			t.Cleanup(func() {
				DB = previousDB
				common.SetDatabaseTypes(previousMainType, previousLogType)
				require.NoError(t, db.Migrator().DropTable(&UserSubscription{}, &SubscriptionPlan{}))
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}))

			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			now := GetDBTimestamp()
			plan := &SubscriptionPlan{
				Id:               9761,
				Title:            "Matrix",
				PriceAmount:      10,
				DurationUnit:     SubscriptionDurationMonth,
				DurationValue:    1,
				TotalAmount:      1000,
				QuotaResetPeriod: SubscriptionResetNever,
			}
			seedSubscriptionResetPlan(t, plan)
			seedSubscriptionResetSub(t, &UserSubscription{Id: 9771, UserId: 601, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 100, StartTime: now - 3600, EndTime: now + 30*24*3600, Status: "active"})

			syncNow := GetDBTimestamp()
			updated := 0
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				updated, err = SyncPlanSubscriptionsQuotaSnapshotTx(tx, plan.Id, 2000, syncNow)
				return err
			}))
			assert.Equal(t, 1, updated)
			assert.EqualValues(t, 2000, getSubscriptionResetSub(t, 9771).AmountTotal)

			err = db.Transaction(func(tx *gorm.DB) error {
				_, err := SyncPlanSubscriptionsQuotaSnapshotTx(tx, plan.Id, 50, syncNow)
				return err
			})
			require.Error(t, err)
			assert.EqualValues(t, 2000, getSubscriptionResetSub(t, 9771).AmountTotal)
		})
	}
}

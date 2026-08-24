package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelRPMSelectionTest(t *testing.T, memoryCacheEnabled bool) *gorm.DB {
	t.Helper()
	originalDB := DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	common.MemoryCacheEnabled = memoryCacheEnabled

	t.Cleanup(func() {
		DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		if originalMemoryCacheEnabled && originalDB != nil &&
			originalDB.Migrator().HasTable(&Channel{}) && originalDB.Migrator().HasTable(&Ability{}) {
			InitChannelCache()
		}
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func addChannelRPMSelectionCandidate(t *testing.T, db *gorm.DB, id int, priority int64) {
	t.Helper()
	weight := uint(100)
	channel := &Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      fmt.Sprintf("test-key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Weight:   &weight,
		Models:   "rpm-test-model",
		Group:    "default",
		Priority: &priority,
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&Ability{
		Group:     "default",
		Model:     "rpm-test-model",
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func TestGetRandomSatisfiedChannelDeprioritizingTriesUnusedCandidate(t *testing.T) {
	for _, memoryCacheEnabled := range []bool{false, true} {
		name := "database"
		if memoryCacheEnabled {
			name = "memory_cache"
		}
		t.Run(name, func(t *testing.T) {
			db := setupChannelRPMSelectionTest(t, memoryCacheEnabled)
			addChannelRPMSelectionCandidate(t, db, 301, 10)
			addChannelRPMSelectionCandidate(t, db, 302, 10)
			addChannelRPMSelectionCandidate(t, db, 303, 0)
			if memoryCacheEnabled {
				InitChannelCache()
			}

			selected, err := GetRandomSatisfiedChannelDeprioritizing(
				"default",
				"rpm-test-model",
				1,
				"/v1/chat/completions",
				map[int]struct{}{301: {}},
			)
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, 302, selected.Id, "another unused channel at the same priority must be selected before lower priorities")

			selected, err = GetRandomSatisfiedChannelDeprioritizing(
				"default",
				"rpm-test-model",
				0,
				"/v1/chat/completions",
				map[int]struct{}{301: {}, 302: {}},
			)
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, 303, selected.Id, "selection must fall through to a lower priority after all higher-priority candidates are excluded")

			selected, err = GetRandomSatisfiedChannelDeprioritizing(
				"default",
				"rpm-test-model",
				1,
				"/v1/chat/completions",
				map[int]struct{}{301: {}, 302: {}, 303: {}},
			)
			require.NoError(t, err)
			require.NotNil(t, selected, "deprioritized channels remain the final fallback when no unused channel exists")
		})
	}
}

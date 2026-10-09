package model

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestBatchSetChannelGroupUpdatesOnlySelectedChannels(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		dialector func(dsn string) gorm.Dialector
	}{
		{name: "sqlite", dialector: func(string) gorm.Dialector { return sqlite.Open(":memory:") }},
		{name: "mysql", env: "TEST_MYSQL_DSN", dialector: func(dsn string) gorm.Dialector { return mysql.Open(dsn) }},
		{name: "postgres", env: "TEST_POSTGRES_DSN", dialector: func(dsn string) gorm.Dialector {
			return postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := ""
			if test.env != "" {
				dsn = strings.TrimSpace(os.Getenv(test.env))
				if dsn == "" {
					t.Skip(test.env + " is not configured")
				}
			}
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{
				NamingStrategy: schema.NamingStrategy{TablePrefix: "channel_group_batch_test_"},
			})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)

			previousDB := DB
			previousCacheEnabled := common.MemoryCacheEnabled
			DB = db
			common.MemoryCacheEnabled = false
			t.Cleanup(func() {
				DB = previousDB
				common.MemoryCacheEnabled = previousCacheEnabled
				require.NoError(t, db.Migrator().DropTable(&Ability{}, &Channel{}))
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))

			priority := int64(1)
			weight := uint(50)
			selectedPrimary := Channel{
				Id:       900101,
				Type:     constant.ChannelTypeOpenAI,
				Key:      "selected-primary",
				Status:   common.ChannelStatusEnabled,
				Name:     "selected-primary",
				Models:   "group-batch-model",
				Group:    "default",
				Priority: &priority,
				Weight:   &weight,
			}
			selectedSecondary := selectedPrimary
			selectedSecondary.Id = 900102
			selectedSecondary.Name = "selected-secondary"
			selectedSecondary.Key = "selected-secondary"
			selectedSecondary.Group = "default,vip"
			untouched := selectedPrimary
			untouched.Id = 900103
			untouched.Name = "untouched"
			untouched.Key = "untouched"

			for _, channel := range []*Channel{&selectedPrimary, &selectedSecondary, &untouched} {
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, channel.UpdateAbilities(nil))
			}

			require.NoError(t, BatchSetChannelGroup(
				[]int{selectedPrimary.Id, selectedSecondary.Id},
				"vip,premium",
			))

			var abilities []Ability
			require.NoError(t, db.Find(&abilities).Error)
			groupsByChannel := make(map[int][]string, len(abilities))
			for _, ability := range abilities {
				groupsByChannel[ability.ChannelId] = append(groupsByChannel[ability.ChannelId], ability.Group)
			}

			for _, id := range []int{selectedPrimary.Id, selectedSecondary.Id} {
				var stored Channel
				require.NoError(t, db.First(&stored, id).Error)
				assert.Equal(t, "vip,premium", stored.Group)
				assert.ElementsMatch(t, []string{"vip", "premium"}, groupsByChannel[id])
			}

			var storedUntouched Channel
			require.NoError(t, db.First(&storedUntouched, untouched.Id).Error)
			assert.Equal(t, "default", storedUntouched.Group)
			assert.ElementsMatch(t, []string{"default"}, groupsByChannel[untouched.Id])
		})
	}
}

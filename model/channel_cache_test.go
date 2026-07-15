package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInitChannelCacheHoldsStatusLockDuringSnapshot(t *testing.T) {
	originalDB := DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	t.Cleanup(func() {
		DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	common.MemoryCacheEnabled = true

	lockObserved := false
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:channel_cache_status_lock",
		func(tx *gorm.DB) {
			if lockObserved || tx.Statement.Table != "channels" {
				return
			}
			lockObserved = true
			if channelStatusLock.TryLock() {
				channelStatusLock.Unlock()
				t.Error("InitChannelCache must hold channelStatusLock while reading its snapshot")
			}
		},
	))

	InitChannelCache()
	require.True(t, lockObserved)
}

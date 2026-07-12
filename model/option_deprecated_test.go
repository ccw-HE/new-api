package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLoadOptionsPurgesDeprecatedSchedulerAttemptLimit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:deprecated-option?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))

	oldDB := DB
	DB = db
	t.Cleanup(func() { DB = oldDB })

	common.OptionMapRWMutex.Lock()
	oldOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	const deprecatedKey = "channel_scheduler_setting.max_attempts_per_request"
	require.NoError(t, db.Create(&Option{Key: deprecatedKey, Value: "5"}).Error)

	loadOptionsFromDatabase()

	common.OptionMapRWMutex.RLock()
	_, exposed := common.OptionMap[deprecatedKey]
	common.OptionMapRWMutex.RUnlock()
	assert.False(t, exposed)

	var count int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", deprecatedKey).Count(&count).Error)
	assert.Zero(t, count)

	require.NoError(t, UpdateOption(deprecatedKey, "9"))
	assertDeprecatedOptionAbsent(t, db, deprecatedKey)

	require.NoError(t, UpdateOptionsBulk(map[string]string{deprecatedKey: "10"}))
	assertDeprecatedOptionAbsent(t, db, deprecatedKey)
}

func assertDeprecatedOptionAbsent(t *testing.T, db *gorm.DB, key string) {
	t.Helper()
	common.OptionMapRWMutex.RLock()
	_, exposed := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	assert.False(t, exposed)

	var count int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", key).Count(&count).Error)
	assert.Zero(t, count)
}

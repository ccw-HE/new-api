package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

const schedulerTestModel = "scheduler-test-model"

func schedulerCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM channels")
		model.DB.Exec("DELETE FROM abilities")
		model.DB.Exec("DELETE FROM channel_scheduler_logs")
	})
}

func TestSchedulerRetryJitterDisabled(t *testing.T) {
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.RetryJitterMinMilliseconds = 0
		s.RetryJitterMaxMilliseconds = 0
	})

	minDelay, maxDelay := operation_setting.GetChannelSchedulerSetting().RetryJitterRange()
	assert.Zero(t, minDelay)
	assert.Zero(t, maxDelay)
}

// withSchedulerSetting 修改全局调度器配置并在测试结束后还原。
func withSchedulerSetting(t *testing.T, mutate func(s *operation_setting.ChannelSchedulerSetting)) {
	t.Helper()
	setting := operation_setting.GetChannelSchedulerSetting()
	saved := *setting
	mutate(setting)
	t.Cleanup(func() {
		*operation_setting.GetChannelSchedulerSetting() = saved
	})
}

type seedChannelOptions struct {
	id       int
	name     string
	priority int64
	status   int
	autoBan  int
	// 渠道级调度配置，nil 表示继承全局
	retryTimes           *int
	disableSeconds       *int
	autoRecoverEnabled   *bool
	manualRestoreAllowed *bool
	autoDisabledUntil    int64
}

func seedSchedulerChannel(t *testing.T, opts seedChannelOptions) *model.Channel {
	t.Helper()
	if opts.status == 0 {
		opts.status = common.ChannelStatusEnabled
	}
	channel := &model.Channel{
		Id:                            opts.id,
		Name:                          opts.name,
		Type:                          1,
		Key:                           "sk-test",
		Status:                        opts.status,
		Priority:                      common.GetPointer(opts.priority),
		Weight:                        common.GetPointer[uint](0),
		Models:                        schedulerTestModel,
		Group:                         "default",
		AutoBan:                       common.GetPointer(opts.autoBan),
		SchedulerRetryTimes:           opts.retryTimes,
		SchedulerAutoDisableSeconds:   opts.disableSeconds,
		SchedulerAutoRecoverEnabled:   opts.autoRecoverEnabled,
		SchedulerManualRestoreAllowed: opts.manualRestoreAllowed,
		AutoDisabledUntil:             opts.autoDisabledUntil,
	}
	require.NoError(t, model.DB.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	return channel
}

func newSchedulerTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func newSchedulerSessionForTest(t *testing.T) *ChannelSchedulerSession {
	t.Helper()
	c := newSchedulerTestContext(t)
	session, err := NewChannelSchedulerSession(c, "default", schedulerTestModel, "")
	require.NoError(t, err)
	return session
}

func mockUpstreamError() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New("mock upstream failure"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
}

func countSchedulerLogs(t *testing.T, eventType string, channelId int) int64 {
	t.Helper()
	var count int64
	tx := model.DB.Model(&model.ChannelSchedulerLog{}).Where("event_type = ?", eventType)
	if channelId != 0 {
		tx = tx.Where("channel_id = ?", channelId)
	}
	require.NoError(t, tx.Count(&count).Error)
	return count
}

func reloadChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	channel, err := model.GetChannelById(id, true)
	require.NoError(t, err)
	return channel
}

func abilityEnabled(t *testing.T, channelId int) bool {
	t.Helper()
	var ability model.Ability
	require.NoError(t, model.DB.Where("channel_id = ?", channelId).First(&ability).Error)
	return ability.Enabled
}

// ---------------------------------------------------------------------------
// 候选分桶：缓存与直查两条路径顺序一致
// ---------------------------------------------------------------------------

func TestGetSatisfiedChannelBucketsBothPaths(t *testing.T) {
	schedulerCleanup(t)
	seedSchedulerChannel(t, seedChannelOptions{id: 101, name: "A", priority: 3, autoBan: 1})
	seedSchedulerChannel(t, seedChannelOptions{id: 102, name: "B", priority: 3, autoBan: 1})
	seedSchedulerChannel(t, seedChannelOptions{id: 103, name: "C", priority: 2, autoBan: 1})
	seedSchedulerChannel(t, seedChannelOptions{id: 104, name: "D", priority: 1, autoBan: 1})
	// 禁用渠道不应出现在候选中
	seedSchedulerChannel(t, seedChannelOptions{id: 105, name: "E", priority: 3, autoBan: 1, status: common.ChannelStatusManuallyDisabled})

	assertBuckets := func(t *testing.T, buckets []*model.ChannelPriorityBucket) {
		require.Len(t, buckets, 3)
		assert.EqualValues(t, 3, buckets[0].Priority)
		assert.EqualValues(t, 2, buckets[1].Priority)
		assert.EqualValues(t, 1, buckets[2].Priority)
		ids := []int{}
		for _, ch := range buckets[0].Channels {
			ids = append(ids, ch.Id)
		}
		assert.ElementsMatch(t, []int{101, 102}, ids)
		require.Len(t, buckets[1].Channels, 1)
		assert.Equal(t, 103, buckets[1].Channels[0].Id)
		require.Len(t, buckets[2].Channels, 1)
		assert.Equal(t, 104, buckets[2].Channels[0].Id)
	}

	t.Run("db_path", func(t *testing.T) {
		require.False(t, common.MemoryCacheEnabled)
		buckets, err := model.GetSatisfiedChannelBuckets("default", schedulerTestModel, "")
		require.NoError(t, err)
		assertBuckets(t, buckets)
	})

	t.Run("cache_path", func(t *testing.T) {
		common.MemoryCacheEnabled = true
		t.Cleanup(func() { common.MemoryCacheEnabled = false })
		model.InitChannelCache()
		buckets, err := model.GetSatisfiedChannelBuckets("default", schedulerTestModel, "")
		require.NoError(t, err)
		assertBuckets(t, buckets)
	})
}

// ---------------------------------------------------------------------------
// 核心失效转移：A×3 -> 禁用 -> B×3 -> 禁用 -> C×3 -> D×3 -> 耗尽
// ---------------------------------------------------------------------------

func TestSchedulerSessionFailoverSequence(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.Enabled = true
		s.ChannelFailureThreshold = 3
		s.AutoDisableSeconds = 7200
		s.MaxAttemptsPerRequest = 50
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 201, name: "A", priority: 3, autoBan: 1})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 202, name: "B", priority: 3, autoBan: 1})
	chC := seedSchedulerChannel(t, seedChannelOptions{id: 203, name: "C", priority: 2, autoBan: 1})
	chD := seedSchedulerChannel(t, seedChannelOptions{id: 204, name: "D", priority: 1, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	before := common.GetTimestamp()

	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	require.Equal(t, chA.Id, channel.Id)

	var sequence []int
	for {
		sequence = append(sequence, channel.Id)
		session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
		next, ok := session.NextChannel()
		if !ok {
			break
		}
		channel = next
	}

	// 期望顺序：同渠道连续 3 次，同级切换后再降级
	require.Equal(t, []int{
		chA.Id, chA.Id, chA.Id,
		chB.Id, chB.Id, chB.Id,
		chC.Id, chC.Id, chC.Id,
		chD.Id, chD.Id, chD.Id,
	}, sequence)

	after := common.GetTimestamp()
	for _, id := range []int{chA.Id, chB.Id, chC.Id, chD.Id} {
		reloaded := reloadChannel(t, id)
		assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status, "channel %d should be auto disabled", id)
		assert.GreaterOrEqual(t, reloaded.AutoDisabledUntil, before+7200)
		assert.LessOrEqual(t, reloaded.AutoDisabledUntil, after+7200)
		assert.False(t, abilityEnabled(t, id), "ability of channel %d should be disabled", id)
		statusReason, ok := reloaded.GetOtherInfo()["status_reason"].(string)
		require.True(t, ok)
		assert.Contains(t, statusReason, "channel scheduler")
	}

	assert.EqualValues(t, 12, countSchedulerLogs(t, model.SchedulerEventFailure, 0))
	assert.EqualValues(t, 4, countSchedulerLogs(t, model.SchedulerEventAutoDisable, 0))
	assert.EqualValues(t, 1, countSchedulerLogs(t, model.SchedulerEventAutoDisable, chA.Id))
}

// 两次失败不禁用
func TestSchedulerSessionTwoFailuresNoDisable(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 3
		s.MaxAttemptsPerRequest = 12
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 211, name: "A", priority: 3, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)

	reloaded := reloadChannel(t, chA.Id)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.EqualValues(t, 0, reloaded.AutoDisabledUntil)
	assert.EqualValues(t, 2, countSchedulerLogs(t, model.SchedulerEventFailure, chA.Id))
	assert.EqualValues(t, 0, countSchedulerLogs(t, model.SchedulerEventAutoDisable, chA.Id))

	// 未达阈值时继续选同一渠道
	next, ok := session.NextChannel()
	require.True(t, ok)
	assert.Equal(t, chA.Id, next.Id)
}

// auto_ban=false：不禁用但记录 observe_disable 日志，且会话内排除
func TestSchedulerSessionAutoBanFalse(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 3
		s.RespectAutoBan = true
		s.MaxAttemptsPerRequest = 12
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 221, name: "A", priority: 3, autoBan: 0})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 222, name: "B", priority: 3, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	for i := 0; i < 3; i++ {
		session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
	}

	reloaded := reloadChannel(t, chA.Id)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.EqualValues(t, 0, reloaded.AutoDisabledUntil)
	assert.EqualValues(t, 1, countSchedulerLogs(t, model.SchedulerEventObserveDisable, chA.Id))
	assert.EqualValues(t, 0, countSchedulerLogs(t, model.SchedulerEventAutoDisable, chA.Id))

	next, ok := session.NextChannel()
	require.True(t, ok)
	assert.Equal(t, chB.Id, next.Id)
}

// RespectAutoBan=false 时无视渠道 auto_ban，照常临时禁用
func TestSchedulerSessionIgnoreAutoBan(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 2
		s.RespectAutoBan = false
		s.MaxAttemptsPerRequest = 12
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 231, name: "A", priority: 3, autoBan: 0})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)

	reloaded := reloadChannel(t, chA.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)
	assert.Greater(t, reloaded.AutoDisabledUntil, int64(0))
}

// 不可重试错误：只记 failure 日志，不排除、不禁用
func TestSchedulerSessionNonRetryableFailure(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 1
		s.MaxAttemptsPerRequest = 12
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 241, name: "A", priority: 3, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureStop)

	reloaded := reloadChannel(t, chA.Id)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.EqualValues(t, 1, countSchedulerLogs(t, model.SchedulerEventFailure, chA.Id))
	assert.EqualValues(t, 0, countSchedulerLogs(t, model.SchedulerEventAutoDisable, chA.Id))
	assert.EqualValues(t, 0, countSchedulerLogs(t, model.SchedulerEventObserveDisable, chA.Id))
}

func TestSchedulerSessionImmediateFailoverTemporarilyDisablesChannel(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 3
		s.AutoDisableSeconds = 7200
		s.MaxAttemptsPerRequest = 12
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{
		id:             243,
		name:           "A",
		priority:       3,
		autoBan:        1,
		disableSeconds: common.GetPointer(600),
	})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 244, name: "B", priority: 3, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	before := common.GetTimestamp()
	err := types.NewOpenAIError(errors.New("gateway timeout"), types.ErrorCodeBadResponseStatusCode, 524)

	session.RecordFailure(channel, err, SchedulerFailureFailoverNow)

	after := common.GetTimestamp()
	reloaded := reloadChannel(t, chA.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)
	assert.GreaterOrEqual(t, reloaded.AutoDisabledUntil, before+600)
	assert.LessOrEqual(t, reloaded.AutoDisabledUntil, after+600)
	assert.False(t, abilityEnabled(t, chA.Id))
	assert.EqualValues(t, 1, countSchedulerLogs(t, model.SchedulerEventAutoDisable, chA.Id))

	next, ok := session.NextChannel()
	require.True(t, ok)
	assert.Equal(t, chB.Id, next.Id)
}

func TestSchedulerSessionDoRequestFailedDoesNotTempDisableChannel(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 1
		s.MaxAttemptsPerRequest = 12
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 245, name: "A", priority: 3, autoBan: 1})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 246, name: "B", priority: 3, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	err := types.NewErrorWithStatusCode(errors.New("dial tcp: connection refused"), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	session.RecordFailure(channel, err, SchedulerFailureFailoverWithoutDisable)

	assert.Equal(t, common.ChannelStatusEnabled, reloadChannel(t, chA.Id).Status)
	assert.EqualValues(t, 1, countSchedulerLogs(t, model.SchedulerEventFailure, chA.Id))
	assert.EqualValues(t, 0, countSchedulerLogs(t, model.SchedulerEventAutoDisable, chA.Id))
	assert.EqualValues(t, 0, countSchedulerLogs(t, model.SchedulerEventObserveDisable, chA.Id))

	next, ok := session.NextChannel()
	require.True(t, ok)
	assert.Equal(t, chB.Id, next.Id)
	assert.Equal(t, common.ChannelStatusEnabled, reloadChannel(t, chB.Id).Status)
}

// 单请求最大尝试次数
func TestSchedulerSessionMaxAttempts(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 100
		s.MaxAttemptsPerRequest = 5
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 251, name: "A", priority: 3, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)

	attempts := 0
	for {
		attempts++
		session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
		next, ok := session.NextChannel()
		if !ok {
			break
		}
		channel = next
	}
	assert.Equal(t, 5, attempts)
	assert.Equal(t, 0, session.RemainingAttempts())
}

func TestSchedulerSessionLowConfiguredMaxAttemptsDoesNotCutOffFailover(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 3
		s.MaxAttemptsPerRequest = 3
		s.AllowPriorityFallback = true
		s.RetrySameChannel = true
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 256, name: "A", priority: 3, autoBan: 1})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 257, name: "B", priority: 3, autoBan: 1})
	chC := seedSchedulerChannel(t, seedChannelOptions{id: 258, name: "C", priority: 2, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)

	var sequence []int
	for {
		sequence = append(sequence, channel.Id)
		session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
		next, ok := session.NextChannel()
		if !ok {
			break
		}
		channel = next
	}

	assert.Equal(t, []int{
		chA.Id, chA.Id, chA.Id,
		chB.Id, chB.Id, chB.Id,
		chC.Id, chC.Id, chC.Id,
	}, sequence)
}

// 渠道级阈值与禁用时长覆盖全局
func TestSchedulerSessionChannelLevelOverrides(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 3
		s.AutoDisableSeconds = 7200
		s.MaxAttemptsPerRequest = 20
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{
		id: 261, name: "A", priority: 3, autoBan: 1,
		retryTimes:     common.GetPointer(2),
		disableSeconds: common.GetPointer(600),
	})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 262, name: "B", priority: 3, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	before := common.GetTimestamp()
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)

	// A 阈值为 2：两次失败即禁用 600 秒
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)

	reloadedA := reloadChannel(t, chA.Id)
	after := common.GetTimestamp()
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadedA.Status)
	assert.GreaterOrEqual(t, reloadedA.AutoDisabledUntil, before+600)
	assert.LessOrEqual(t, reloadedA.AutoDisabledUntil, after+600)

	// B 未配置渠道级，用全局阈值 3
	next, ok := session.NextChannel()
	require.True(t, ok)
	require.Equal(t, chB.Id, next.Id)
	session.RecordFailure(next, mockUpstreamError(), SchedulerFailureRetryCurrent)
	session.RecordFailure(next, mockUpstreamError(), SchedulerFailureRetryCurrent)
	assert.Equal(t, common.ChannelStatusEnabled, reloadChannel(t, chB.Id).Status)
	session.RecordFailure(next, mockUpstreamError(), SchedulerFailureRetryCurrent)
	reloadedB := reloadChannel(t, chB.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadedB.Status)
	assert.GreaterOrEqual(t, reloadedB.AutoDisabledUntil, before+7200)
}

// 关闭同渠道连续重试：优先换渠道；同级仅剩当前渠道时继续使用它而不是放弃
func TestSchedulerSessionRetrySameChannelDisabled(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 10
		s.RetrySameChannel = false
		s.AllowPriorityFallback = false
		s.MaxAttemptsPerRequest = 20
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 341, name: "A", priority: 3, autoBan: 1})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 342, name: "B", priority: 3, autoBan: 1})
	seedSchedulerChannel(t, seedChannelOptions{id: 343, name: "C", priority: 2, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)

	// A 失败一次后应换到同级 B
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
	next, ok := session.NextChannel()
	require.True(t, ok)
	assert.Equal(t, chB.Id, next.Id)

	// B 被排除（如装配失败）后，同级仅剩 A：应继续使用 A 而不是降级或放弃
	session.ExcludeChannel(chB.Id)
	session.RecordFailure(chA, mockUpstreamError(), SchedulerFailureRetryCurrent)
	next, ok = session.NextChannel()
	require.True(t, ok, "sole remaining same-priority channel must stay usable")
	assert.Equal(t, chA.Id, next.Id)
}

// 候选过滤跳过已被并发禁用的渠道
func TestSchedulerSessionSkipsConcurrentlyDisabledCandidates(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 1
		s.MaxAttemptsPerRequest = 10
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 351, name: "A", priority: 3, autoBan: 1})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 352, name: "B", priority: 3, autoBan: 1})
	chC := seedSchedulerChannel(t, seedChannelOptions{id: 353, name: "C", priority: 2, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent) // A 达到阈值被禁用

	// 模拟并发请求把 B 手动禁用（会话候选快照中的 B 状态同步变更）
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", chB.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	for _, group := range sessionGroupsForTest(session) {
		for _, bucket := range group.buckets {
			for _, candidate := range bucket.Channels {
				if candidate.Id == chB.Id {
					candidate.Status = common.ChannelStatusManuallyDisabled
				}
			}
		}
	}

	next, ok := session.NextChannel()
	require.True(t, ok)
	assert.Equal(t, chC.Id, next.Id, "disabled B should be skipped, fallback to C")
}

func sessionGroupsForTest(s *ChannelSchedulerSession) []schedulerGroupCandidates {
	return s.groups
}

// 原子恢复：未到期的临时禁用不会被自动恢复路径覆盖（恢复任务与新一轮禁用竞争的保护）
func TestSchedulerRecoverChannelAtomicGuards(t *testing.T) {
	schedulerCleanup(t)
	now := common.GetTimestamp()
	fresh := seedSchedulerChannel(t, seedChannelOptions{id: 361, name: "fresh", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now + 3000})

	// requireExpired=true（自动恢复）：未到期必须拒绝
	assert.False(t, model.SchedulerRecoverChannel(fresh.Id, true))
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, fresh.Id).Status)

	// requireExpired=false（手动恢复）：允许
	assert.True(t, model.SchedulerRecoverChannel(fresh.Id, false))
	reloaded := reloadChannel(t, fresh.Id)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status)
	assert.EqualValues(t, 0, reloaded.AutoDisabledUntil)

	// 临时禁用的 CAS 前置：非启用状态不会被覆盖
	manual := seedSchedulerChannel(t, seedChannelOptions{id: 362, name: "manual", priority: 3, autoBan: 1, status: common.ChannelStatusManuallyDisabled})
	assert.False(t, model.SchedulerTempDisableChannel(manual.Id, "test", now+100))
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadChannel(t, manual.Id).Status)
}

// 关闭降级：同级耗尽后不落到低优先级
func TestSchedulerSessionNoPriorityFallback(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 1
		s.AllowPriorityFallback = false
		s.MaxAttemptsPerRequest = 20
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 271, name: "A", priority: 3, autoBan: 1})
	seedSchedulerChannel(t, seedChannelOptions{id: 272, name: "C", priority: 2, autoBan: 1})

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)

	_, ok := session.NextChannel()
	assert.False(t, ok, "should not fall back to lower priority when disabled")
}

// 内存缓存路径下的失效转移与缓存状态同步
func TestSchedulerSessionFailoverWithMemoryCache(t *testing.T) {
	schedulerCleanup(t)
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.ChannelFailureThreshold = 2
		s.AutoDisableSeconds = 7200
		s.MaxAttemptsPerRequest = 20
	})
	chA := seedSchedulerChannel(t, seedChannelOptions{id: 281, name: "A", priority: 3, autoBan: 1})
	chB := seedSchedulerChannel(t, seedChannelOptions{id: 282, name: "B", priority: 3, autoBan: 1})

	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = false })
	model.InitChannelCache()

	session := newSchedulerSessionForTest(t)
	channel := session.AdoptInitialChannel(chA.Id)
	require.NotNil(t, channel)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)
	session.RecordFailure(channel, mockUpstreamError(), SchedulerFailureRetryCurrent)

	// DB 与缓存都应看到禁用状态与到期时间
	reloaded := reloadChannel(t, chA.Id)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloaded.Status)
	cached, err := model.CacheGetChannel(chA.Id)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, cached.Status)
	assert.Equal(t, reloaded.AutoDisabledUntil, cached.AutoDisabledUntil)

	next, ok := session.NextChannel()
	require.True(t, ok)
	assert.Equal(t, chB.Id, next.Id)
}

// ---------------------------------------------------------------------------
// 自动恢复与手动恢复
// ---------------------------------------------------------------------------

func TestRecoverExpiredSchedulerChannels(t *testing.T) {
	schedulerCleanup(t)
	now := common.GetTimestamp()
	// 到期，应恢复
	expired := seedSchedulerChannel(t, seedChannelOptions{id: 301, name: "expired", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now - 100})
	// 未到期，不恢复
	future := seedSchedulerChannel(t, seedChannelOptions{id: 302, name: "future", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now + 9999})
	// 手动禁用（即使误带到期时间也不恢复）
	manual := seedSchedulerChannel(t, seedChannelOptions{id: 303, name: "manual", priority: 3, autoBan: 1, status: common.ChannelStatusManuallyDisabled, autoDisabledUntil: now - 100})
	// 旧式 auto disabled（无到期时间）不恢复
	legacy := seedSchedulerChannel(t, seedChannelOptions{id: 304, name: "legacy", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: 0})
	// 渠道级关闭自动恢复
	noRecover := seedSchedulerChannel(t, seedChannelOptions{id: 305, name: "no-recover", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now - 100, autoRecoverEnabled: common.GetPointer(false)})

	recovered, err := RecoverExpiredSchedulerChannels()
	require.NoError(t, err)
	assert.Equal(t, 1, recovered)

	assert.Equal(t, common.ChannelStatusEnabled, reloadChannel(t, expired.Id).Status)
	assert.EqualValues(t, 0, reloadChannel(t, expired.Id).AutoDisabledUntil)
	assert.True(t, abilityEnabled(t, expired.Id))

	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, future.Id).Status)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadChannel(t, manual.Id).Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, legacy.Id).Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, noRecover.Id).Status)

	assert.EqualValues(t, 1, countSchedulerLogs(t, model.SchedulerEventAutoRecover, expired.Id))

	// 关闭自动恢复的渠道可以手动恢复
	require.NoError(t, ManualRestoreSchedulerChannel(noRecover.Id, 1, "root"))
	assert.Equal(t, common.ChannelStatusEnabled, reloadChannel(t, noRecover.Id).Status)
	assert.EqualValues(t, 1, countSchedulerLogs(t, model.SchedulerEventManualRestore, noRecover.Id))
}

func TestManualRestoreRules(t *testing.T) {
	schedulerCleanup(t)
	now := common.GetTimestamp()
	// 不允许手动恢复
	blocked := seedSchedulerChannel(t, seedChannelOptions{id: 311, name: "blocked", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now + 5000, manualRestoreAllowed: common.GetPointer(false)})
	notExpired := seedSchedulerChannel(t, seedChannelOptions{id: 313, name: "not-expired", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now + 5000})
	// 手动禁用渠道不属于调度器临时禁用
	manual := seedSchedulerChannel(t, seedChannelOptions{id: 312, name: "manual", priority: 3, autoBan: 1, status: common.ChannelStatusManuallyDisabled})

	err := ManualRestoreSchedulerChannel(blocked.Id, 1, "root")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "手动恢复")
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, blocked.Id).Status)

	err = ManualRestoreSchedulerChannel(notExpired.Id, 1, "root")
	require.Error(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, reloadChannel(t, notExpired.Id).Status)
	assert.EqualValues(t, 0, countSchedulerLogs(t, model.SchedulerEventManualRestore, notExpired.Id))

	err = ManualRestoreSchedulerChannel(manual.Id, 1, "root")
	require.Error(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadChannel(t, manual.Id).Status)
}

func TestHasSchedulerTempDisabledChannelsRequiresRecoverableChannel(t *testing.T) {
	schedulerCleanup(t)
	now := common.GetTimestamp()
	seedSchedulerChannel(t, seedChannelOptions{id: 314, name: "future", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now + 5000})
	seedSchedulerChannel(t, seedChannelOptions{id: 315, name: "no-auto-recover", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now - 100, autoRecoverEnabled: common.GetPointer(false)})
	assert.False(t, model.HasSchedulerTempDisabledChannels())

	seedSchedulerChannel(t, seedChannelOptions{id: 316, name: "recoverable", priority: 3, autoBan: 1, status: common.ChannelStatusAutoDisabled, autoDisabledUntil: now - 100})
	assert.True(t, model.HasSchedulerTempDisabledChannels())
}

// 临时禁用不得覆盖手动禁用（并发保护）
func TestTempDisableDoesNotOverrideManualDisable(t *testing.T) {
	schedulerCleanup(t)
	manual := seedSchedulerChannel(t, seedChannelOptions{id: 321, name: "manual", priority: 3, autoBan: 1, status: common.ChannelStatusManuallyDisabled})

	until, disabled := TempDisableChannelForScheduler(manual, mockUpstreamError(), 7200)
	assert.False(t, disabled)
	assert.EqualValues(t, 0, until)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloadChannel(t, manual.Id).Status)
	assert.EqualValues(t, 0, reloadChannel(t, manual.Id).AutoDisabledUntil)
}

// ---------------------------------------------------------------------------
// 模式判定
// ---------------------------------------------------------------------------

func TestShouldUseChannelScheduler(t *testing.T) {
	tests := []struct {
		name              string
		enabled           bool
		isStream          bool
		specificChannelId bool
		expected          bool
	}{
		{name: "disabled", enabled: false, expected: false},
		{name: "driving", enabled: true, expected: true},
		{name: "stream_uses_scheduler_when_enabled", enabled: true, isStream: true, expected: true},
		{name: "specific_channel", enabled: true, specificChannelId: true, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
				s.Enabled = tt.enabled
			})
			c := newSchedulerTestContext(t)
			if tt.specificChannelId {
				c.Set("specific_channel_id", "1")
			}
			assert.Equal(t, tt.expected, ShouldUseChannelScheduler(c, tt.isStream))
		})
	}
}

func TestSchedulerRetryJitterRange(t *testing.T) {
	withSchedulerSetting(t, func(s *operation_setting.ChannelSchedulerSetting) {
		s.RetryJitterMinMilliseconds = 3000
		s.RetryJitterMaxMilliseconds = 7000
	})

	minDelay, maxDelay := operation_setting.GetChannelSchedulerSetting().RetryJitterRange()
	assert.Equal(t, 3*time.Second, minDelay)
	assert.Equal(t, 7*time.Second, maxDelay)

	for i := 0; i < 20; i++ {
		delay := randomSchedulerRetryJitter(minDelay, maxDelay)
		assert.GreaterOrEqual(t, delay, minDelay)
		assert.LessOrEqual(t, delay, maxDelay)
	}
}

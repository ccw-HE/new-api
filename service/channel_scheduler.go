package service

import (
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// 高级渠道调度器（ChannelScheduler）。
//
// 与旧调度语义的区别：旧逻辑 retry 下标对应第 N 个优先级层级；
// 高级调度器在同一优先级内让单个渠道连续重试到阈值，达到阈值后临时禁用该渠道
// 并换同优先级其他渠道，同级全部耗尽后才降级到下一优先级。
//
// 失败计数为单次请求内计数（不做跨节点全局计数），并发一致性依赖
// model.UpdateChannelStatus 的幂等语义与会话本地排除集。

// ShouldUseChannelScheduler 判断当前请求是否由高级调度器接管渠道选择。
func ShouldUseChannelScheduler(c *gin.Context, isStream bool) bool {
	s := operation_setting.GetChannelSchedulerSetting()
	if !s.Enabled {
		return false
	}
	if isStream && !s.EnableForStream {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	return true
}

type schedulerGroupCandidates struct {
	name    string
	buckets []*model.ChannelPriorityBucket
}

// ChannelSchedulerSession 单次请求的调度会话。
// 非并发安全：一个请求内串行使用。
type ChannelSchedulerSession struct {
	ctx           *gin.Context
	setting       operation_setting.ChannelSchedulerSetting
	tokenGroup    string
	modelName     string
	requestPath   string
	groups        []schedulerGroupCandidates
	groupIdx      int
	bucketIdx     int
	current       *model.Channel
	currentGroup  string
	failures      map[int]int
	excluded      map[int]bool
	totalAttempts int
}

// NewChannelSchedulerSession 创建会话并加载候选渠道。
// auto 分组时按用户 auto 分组顺序展开；未开启跨分组重试时只保留
// 第一个有候选渠道的分组，与旧逻辑保持一致。
func NewChannelSchedulerSession(c *gin.Context, tokenGroup string, modelName string, requestPath string) (*ChannelSchedulerSession, error) {
	session := &ChannelSchedulerSession{
		ctx:         c,
		setting:     *operation_setting.GetChannelSchedulerSetting(),
		tokenGroup:  tokenGroup,
		modelName:   modelName,
		requestPath: requestPath,
		failures:    make(map[int]int),
		excluded:    make(map[int]bool),
	}
	if tokenGroup == "auto" {
		userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		autoGroups := GetUserAutoGroup(userGroup)
		if len(autoGroups) == 0 {
			return nil, errors.New("auto groups is not enabled")
		}
		crossGroupRetry := common.GetContextKeyBool(c, constant.ContextKeyTokenCrossGroupRetry)
		for _, group := range autoGroups {
			buckets, err := model.GetSatisfiedChannelBuckets(group, modelName, requestPath)
			if err != nil {
				return nil, err
			}
			if len(buckets) == 0 {
				continue
			}
			session.groups = append(session.groups, schedulerGroupCandidates{name: group, buckets: buckets})
			if !crossGroupRetry {
				break
			}
		}
	} else {
		buckets, err := model.GetSatisfiedChannelBuckets(tokenGroup, modelName, requestPath)
		if err != nil {
			return nil, err
		}
		if len(buckets) > 0 {
			session.groups = append(session.groups, schedulerGroupCandidates{name: tokenGroup, buckets: buckets})
		}
	}
	return session, nil
}

// AdoptInitialChannel 把 middleware.Distribute 已选好的首个渠道纳入会话，
// 并把会话游标对齐到该渠道所在的分组与优先级桶。
func (s *ChannelSchedulerSession) AdoptInitialChannel(channelId int) *model.Channel {
	channel, err := model.CacheGetChannel(channelId)
	if err != nil || channel == nil {
		return nil
	}
	s.current = channel
	s.currentGroup = s.tokenGroup
	if s.tokenGroup == "auto" {
		if autoGroup := common.GetContextKeyString(s.ctx, constant.ContextKeyAutoGroup); autoGroup != "" {
			s.currentGroup = autoGroup
		}
	}
	for gi, group := range s.groups {
		if s.tokenGroup == "auto" && group.name != s.currentGroup {
			continue
		}
		for bi, bucket := range group.buckets {
			for _, candidate := range bucket.Channels {
				if candidate.Id == channelId {
					s.groupIdx = gi
					s.bucketIdx = bi
					return channel
				}
			}
		}
	}
	// 首选渠道不在候选集中（缓存刚刷新、affinity 指定等），从头开始遍历。
	s.groupIdx = 0
	s.bucketIdx = 0
	return channel
}

// NextChannel 返回下一个应尝试的渠道。
// 返回 false 表示候选耗尽或达到单请求最大尝试次数，应停止重试。
func (s *ChannelSchedulerSession) NextChannel() (*model.Channel, bool) {
	if s.totalAttempts >= s.setting.MaxAttemptsPerRequest {
		return nil, false
	}
	// 同渠道连续重试：未被排除前继续使用当前渠道
	if s.current != nil && s.setting.RetrySameChannel && !s.excluded[s.current.Id] {
		return s.current, true
	}
	for s.groupIdx < len(s.groups) {
		group := s.groups[s.groupIdx]
		for s.bucketIdx < len(group.buckets) {
			bucket := group.buckets[s.bucketIdx]
			candidates := make([]*model.Channel, 0, len(bucket.Channels))
			for _, candidate := range bucket.Channels {
				if s.excluded[candidate.Id] {
					continue
				}
				// 过滤已被并发请求禁用的渠道，减少注定失败的尝试
				if candidate.Status != common.ChannelStatusEnabled {
					continue
				}
				candidates = append(candidates, candidate)
			}
			// 关闭同渠道连续重试时优先换一个渠道；
			// 以过滤后的可用候选数判断，仅剩当前渠道时仍继续使用它
			if !s.setting.RetrySameChannel && s.current != nil && len(candidates) > 1 {
				withoutCurrent := make([]*model.Channel, 0, len(candidates))
				for _, candidate := range candidates {
					if candidate.Id != s.current.Id {
						withoutCurrent = append(withoutCurrent, candidate)
					}
				}
				if len(withoutCurrent) > 0 {
					candidates = withoutCurrent
				}
			}
			if len(candidates) > 0 {
				channel := pickWeightedChannel(candidates)
				s.current = channel
				s.currentGroup = group.name
				if s.tokenGroup == "auto" {
					common.SetContextKey(s.ctx, constant.ContextKeyAutoGroup, group.name)
				}
				return channel, true
			}
			if !s.setting.AllowPriorityFallback {
				return nil, false
			}
			s.bucketIdx++
		}
		s.groupIdx++
		s.bucketIdx = 0
	}
	return nil, false
}

// RemainingAttempts 剩余可用尝试次数，供 shouldRetry 的次数判断使用。
func (s *ChannelSchedulerSession) RemainingAttempts() int {
	remaining := s.setting.MaxAttemptsPerRequest - s.totalAttempts
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *ChannelSchedulerSession) WaitBeforeRetry() *types.NewAPIError {
	minDelay, maxDelay := s.setting.RetryJitterRange()
	delay := randomSchedulerRetryJitter(minDelay, maxDelay)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-s.ctx.Request.Context().Done():
		return types.NewErrorWithStatusCode(s.ctx.Request.Context().Err(), types.ErrorCodeDoRequestFailed, http.StatusRequestTimeout, types.ErrOptionWithSkipRetry())
	}
}

func randomSchedulerRetryJitter(minDelay time.Duration, maxDelay time.Duration) time.Duration {
	if minDelay <= 0 || maxDelay <= 0 {
		return 0
	}
	if maxDelay < minDelay {
		return 0
	}
	if maxDelay == minDelay {
		return minDelay
	}
	return minDelay + time.Duration(rand.Int63n(int64(maxDelay-minDelay)+1))
}

// CurrentGroup 当前选中渠道所属分组（auto 分组场景与 tokenGroup 不同）。
func (s *ChannelSchedulerSession) CurrentGroup() string {
	if s.currentGroup != "" {
		return s.currentGroup
	}
	return s.tokenGroup
}

// ExcludeChannel 把渠道从本会话候选中排除（如上下文装配失败、无可用 key）。
func (s *ChannelSchedulerSession) ExcludeChannel(channelId int) {
	s.excluded[channelId] = true
	if s.current != nil && s.current.Id == channelId {
		s.current = nil
	}
}

// RecordFailure 记录一次渠道失败：
// 1. 失败计数加一并写 failure 调度日志；
// 2. 不可重试错误不触发禁用（请求侧错误不归因渠道）；
// 3. 达到渠道级阈值后将渠道移出本会话候选，并按配置执行临时禁用。
func (s *ChannelSchedulerSession) RecordFailure(channel *model.Channel, apiErr *types.NewAPIError, retryable bool) {
	if channel == nil {
		return
	}
	s.totalAttempts++
	s.failures[channel.Id]++
	attemptCount := s.failures[channel.Id]

	if s.setting.LogEnabled {
		entry := s.buildLogEntry(channel, apiErr)
		entry.EventType = model.SchedulerEventFailure
		entry.AttemptCount = attemptCount
		entry.Metadata = common.MapToJsonStr(map[string]interface{}{
			"total_attempts": s.totalAttempts,
			"retryable":      retryable,
		})
		model.RecordChannelSchedulerLog(entry)
	}

	if !retryable {
		return
	}

	threshold := channel.ResolveSchedulerRetryTimes(s.setting.ChannelFailureThreshold)
	if attemptCount < threshold {
		return
	}

	// 达到阈值：移出本会话候选（无论是否执行数据库层面的禁用）
	s.ExcludeChannel(channel.Id)

	skipReason := ""
	if !channel.GetSchedulerEnabled() {
		skipReason = "channel scheduler disabled (scheduler_enabled=false)"
	} else if s.setting.RespectAutoBan && !channel.GetAutoBan() {
		skipReason = "channel auto_ban disabled"
	}

	if skipReason != "" {
		if s.setting.LogEnabled {
			entry := s.buildLogEntry(channel, apiErr)
			entry.EventType = model.SchedulerEventObserveDisable
			entry.AttemptCount = attemptCount
			entry.Metadata = common.MapToJsonStr(map[string]interface{}{
				"skip_reason":    skipReason,
				"total_attempts": s.totalAttempts,
			})
			model.RecordChannelSchedulerLog(entry)
		}
		return
	}

	seconds := channel.ResolveSchedulerAutoDisableSeconds(s.setting.AutoDisableSeconds)
	disabledUntil, disabled := TempDisableChannelForScheduler(channel, apiErr, seconds)
	if s.setting.LogEnabled {
		entry := s.buildLogEntry(channel, apiErr)
		entry.AttemptCount = attemptCount
		entry.DisableDurationSeconds = seconds
		entry.DisabledUntil = disabledUntil
		if disabled {
			entry.EventType = model.SchedulerEventAutoDisable
		} else {
			entry.EventType = model.SchedulerEventObserveDisable
			entry.Metadata = common.MapToJsonStr(map[string]interface{}{
				"skip_reason": "channel already disabled by concurrent request or status changed",
			})
		}
		model.RecordChannelSchedulerLog(entry)
	}
}

func (s *ChannelSchedulerSession) buildLogEntry(channel *model.Channel, apiErr *types.NewAPIError) *model.ChannelSchedulerLog {
	c := s.ctx
	entry := &model.ChannelSchedulerLog{
		RequestId:   c.GetString(common.RequestIdKey),
		UserId:      c.GetInt("id"),
		Username:    c.GetString("username"),
		TokenId:     c.GetInt("token_id"),
		TokenName:   c.GetString("token_name"),
		Group:       s.CurrentGroup(),
		ModelName:   s.modelName,
		ChannelId:   channel.Id,
		ChannelName: channel.Name,
		ChannelType: channel.Type,
		Priority:    channel.GetPriority(),
	}
	if apiErr != nil {
		entry.StatusCode = apiErr.StatusCode
		entry.ErrorCode = string(apiErr.GetErrorCode())
		entry.ErrorType = string(apiErr.GetErrorType())
		entry.Reason = apiErr.MaskSensitiveError()
	}
	if usedChannels := c.GetStringSlice("use_channel"); len(usedChannels) > 0 {
		if data, err := common.Marshal(usedChannels); err == nil {
			entry.UsedChannels = string(data)
		}
	}
	return entry
}

// TempDisableChannelForScheduler 执行调度器临时禁用：
// status 与 auto_disabled_until 由 model.SchedulerTempDisableChannel 原子写入，
// 同时写 other_info 状态原因、同步 ability 与内存缓存。
// 返回 (到期时间, 是否真正执行了禁用)。前置条件为渠道当前处于启用状态，
// 并发下重复禁用、覆盖手动禁用都会被拒绝（返回 false）。
func TempDisableChannelForScheduler(channel *model.Channel, apiErr *types.NewAPIError, seconds int) (int64, bool) {
	if channel == nil || channel.Id == 0 {
		return 0, false
	}
	reason := "channel scheduler: consecutive failure threshold reached"
	if apiErr != nil {
		reason = fmt.Sprintf("channel scheduler: consecutive failures reached threshold, last error: %s", apiErr.MaskSensitiveErrorWithStatusCode())
	}
	disabledUntil := common.GetTimestamp() + int64(seconds)
	if !model.SchedulerTempDisableChannel(channel.Id, reason, disabledUntil) {
		return 0, false
	}
	common.SysLog(fmt.Sprintf("channel scheduler temporarily disabled channel「%s」(#%d) for %d seconds, until %d", channel.Name, channel.Id, seconds, disabledUntil))
	subject := fmt.Sprintf("渠道「%s」（#%d）已被调度器临时禁用", channel.Name, channel.Id)
	content := fmt.Sprintf("渠道「%s」（#%d）连续失败达到阈值，已被调度器临时禁用 %d 秒，预计 %s 恢复。原因：%s",
		channel.Name, channel.Id, seconds, time.Unix(disabledUntil, 0).Format("2006-01-02 15:04:05"), reason)
	NotifyRootUser(formatNotifyType(channel.Id, common.ChannelStatusAutoDisabled), subject, content)
	return disabledUntil, true
}

// RecoverExpiredSchedulerChannels 恢复所有到期的调度器临时禁用渠道。
// 只恢复 status=auto_disabled 且 auto_disabled_until>0 且已到期的渠道；
// 渠道级关闭自动恢复（scheduler_auto_recover_enabled=false）的跳过，
// 手动禁用与旧式无到期时间的 auto disabled 渠道不受影响。
func RecoverExpiredSchedulerChannels() (int, error) {
	now := common.GetTimestamp()
	channels, err := model.GetExpiredSchedulerDisabledChannels(now)
	if err != nil {
		return 0, err
	}
	recovered := 0
	logEnabled := operation_setting.GetChannelSchedulerSetting().LogEnabled
	for _, channel := range channels {
		if !channel.GetSchedulerAutoRecoverEnabled() {
			continue
		}
		// 原子恢复：锁内重新校验 status/until/到期，避免覆盖并发写入的新一轮禁用
		if !model.SchedulerRecoverChannel(channel.Id, true) {
			continue
		}
		recovered++
		common.SysLog(fmt.Sprintf("channel scheduler auto recovered channel「%s」(#%d)", channel.Name, channel.Id))
		if logEnabled {
			model.RecordChannelSchedulerLog(&model.ChannelSchedulerLog{
				EventType:     model.SchedulerEventAutoRecover,
				ChannelId:     channel.Id,
				ChannelName:   channel.Name,
				ChannelType:   channel.Type,
				Priority:      channel.GetPriority(),
				DisabledUntil: channel.AutoDisabledUntil,
				Reason:        "temporary disable expired, auto recovered",
			})
		}
	}
	if recovered > 0 {
		// CacheUpdateChannelStatus 只会从缓存移除禁用渠道，重新启用需要整体重建
		model.InitChannelCache()
	}
	return recovered, nil
}

// ManualRestoreSchedulerChannel 管理员手动恢复调度器临时禁用的渠道。
func ManualRestoreSchedulerChannel(channelId int, operatorId int, operatorName string) error {
	channel, err := model.GetChannelById(channelId, true)
	if err != nil {
		return err
	}
	if channel.Status != common.ChannelStatusAutoDisabled || channel.AutoDisabledUntil == 0 {
		return errors.New("该渠道不处于调度器临时禁用状态")
	}
	if !channel.GetSchedulerManualRestoreAllowed() {
		return errors.New("该渠道已关闭手动恢复")
	}
	if channel.AutoDisabledUntil > common.GetTimestamp() {
		return errors.New("该渠道临时禁用尚未到期")
	}
	if !model.SchedulerRecoverChannel(channelId, true) {
		return errors.New("恢复渠道失败")
	}
	model.InitChannelCache()
	if operation_setting.GetChannelSchedulerSetting().LogEnabled {
		model.RecordChannelSchedulerLog(&model.ChannelSchedulerLog{
			EventType:     model.SchedulerEventManualRestore,
			UserId:        operatorId,
			Username:      operatorName,
			ChannelId:     channel.Id,
			ChannelName:   channel.Name,
			ChannelType:   channel.Type,
			Priority:      channel.GetPriority(),
			DisabledUntil: channel.AutoDisabledUntil,
			Reason:        "manually restored by admin",
		})
	}
	return nil
}

// pickWeightedChannel 在同优先级候选内按权重随机选择一个渠道。
// 权重算法与 model.GetRandomSatisfiedChannel 的缓存路径保持一致（含平滑因子）。
func pickWeightedChannel(channels []*model.Channel) *model.Channel {
	if len(channels) == 0 {
		return nil
	}
	if len(channels) == 1 {
		return channels[0]
	}
	sumWeight := 0
	for _, channel := range channels {
		sumWeight += channel.GetWeight()
	}
	smoothingFactor := 1
	smoothingAdjustment := 0
	if sumWeight == 0 {
		sumWeight = len(channels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(channels) < 10 {
		smoothingFactor = 100
	}
	totalWeight := sumWeight * smoothingFactor
	randomWeight := rand.Intn(totalWeight)
	for _, channel := range channels {
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel
		}
	}
	return channels[len(channels)-1]
}

// SchedulerDisabledChannelView 临时禁用渠道的对外展示结构（脱敏，不含 key）。
type SchedulerDisabledChannelView struct {
	Id                          int    `json:"id"`
	Name                        string `json:"name"`
	Type                        int    `json:"type"`
	Priority                    int64  `json:"priority"`
	AutoDisabledUntil           int64  `json:"auto_disabled_until"`
	StatusReason                string `json:"status_reason"`
	StatusTime                  int64  `json:"status_time"`
	SchedulerAutoRecoverEnabled bool   `json:"scheduler_auto_recover_enabled"`
	ManualRestoreAllowed        bool   `json:"manual_restore_allowed"`
}

// ListSchedulerDisabledChannels 当前处于调度器临时禁用状态的渠道列表。
func ListSchedulerDisabledChannels() ([]SchedulerDisabledChannelView, error) {
	channels, err := model.GetSchedulerTempDisabledChannels()
	if err != nil {
		return nil, err
	}
	views := make([]SchedulerDisabledChannelView, 0, len(channels))
	for _, channel := range channels {
		view := SchedulerDisabledChannelView{
			Id:                          channel.Id,
			Name:                        channel.Name,
			Type:                        channel.Type,
			Priority:                    channel.GetPriority(),
			AutoDisabledUntil:           channel.AutoDisabledUntil,
			SchedulerAutoRecoverEnabled: channel.GetSchedulerAutoRecoverEnabled(),
			ManualRestoreAllowed:        channel.GetSchedulerManualRestoreAllowed(),
		}
		otherInfo := channel.GetOtherInfo()
		if reason, ok := otherInfo["status_reason"].(string); ok {
			view.StatusReason = strings.TrimSpace(reason)
		}
		if statusTime, ok := otherInfo["status_time"].(float64); ok {
			view.StatusTime = int64(statusTime)
		}
		views = append(views, view)
	}
	return views, nil
}

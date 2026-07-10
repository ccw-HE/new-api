package controller

import (
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

const (
	schedulerConfigPrefix       = "channel_scheduler_setting."
	maxSchedulerRetryTimes      = 100
	maxSchedulerDisableSeconds  = 30 * 24 * 3600
	maxSchedulerAttemptsPerReq  = 10000
	maxSchedulerLogRetention    = 1000
	maxSchedulerRetryJitterMs   = 10000
	minSchedulerDisableSeconds  = 1
	minSchedulerRetryTimes      = 1
	minSchedulerAttemptsPerReq  = 1
	minSchedulerLogRetention    = 1
	minSchedulerRetryJitterMs   = 100
	schedulerRestoreAuditAction = "channel.scheduler_restore"
	schedulerChannelCfgAuditKey = "channel.scheduler_config"
	schedulerGlobalCfgAuditKey  = "scheduler.config_update"
)

// GetChannelSchedulerConfig 查看全局调度器配置（Root）。
func GetChannelSchedulerConfig(c *gin.Context) {
	common.ApiSuccess(c, operation_setting.GetChannelSchedulerSetting())
}

// UpdateChannelSchedulerConfig 保存全局调度器配置（Root）。
func UpdateChannelSchedulerConfig(c *gin.Context) {
	setting := operation_setting.ChannelSchedulerSetting{}
	if err := c.ShouldBindJSON(&setting); err != nil {
		common.ApiError(c, err)
		return
	}
	if setting.ChannelFailureThreshold < minSchedulerRetryTimes || setting.ChannelFailureThreshold > maxSchedulerRetryTimes {
		common.ApiErrorMsg(c, "单渠道失败阈值必须在 1-100 之间")
		return
	}
	if setting.AutoDisableSeconds < minSchedulerDisableSeconds || setting.AutoDisableSeconds > maxSchedulerDisableSeconds {
		common.ApiErrorMsg(c, "临时禁用时长必须在 1 秒到 30 天之间")
		return
	}
	if setting.MaxAttemptsPerRequest < minSchedulerAttemptsPerReq || setting.MaxAttemptsPerRequest > maxSchedulerAttemptsPerReq {
		common.ApiErrorMsg(c, "单请求最大尝试次数必须在 1-10000 之间")
		return
	}
	if setting.SchedulerLogRetentionEnabled && (setting.SchedulerLogRetentionCount < minSchedulerLogRetention || setting.SchedulerLogRetentionCount > maxSchedulerLogRetention) {
		common.ApiErrorMsg(c, "调度日志保留数量必须在 1-1000 之间")
		return
	}
	if !validSchedulerRetryJitter(setting.RetryJitterMinMilliseconds, setting.RetryJitterMaxMilliseconds) {
		common.ApiErrorMsg(c, "重试随机抖动必须为 0/0 关闭，或在 0.1-10 秒之间且最小值不大于最大值")
		return
	}
	previousSetting := *operation_setting.GetChannelSchedulerSetting()
	configMap, err := config.ConfigToMap(&setting)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	values := make(map[string]string, len(configMap))
	for key, value := range configMap {
		values[schedulerConfigPrefix+key] = value
	}
	if err := model.UpdateOptionsBulk(values); err != nil {
		common.ApiError(c, err)
		return
	}
	if !previousSetting.SchedulerLogRetentionEnabled && setting.SchedulerLogRetentionEnabled {
		if err := service.ResetSchedulerLogRetentionBaseline(time.Now()); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	recordManageAudit(c, schedulerGlobalCfgAuditKey, map[string]interface{}{
		"enabled":             setting.Enabled,
		"retry_jitter_min_ms": setting.RetryJitterMinMilliseconds,
		"retry_jitter_max_ms": setting.RetryJitterMaxMilliseconds,
	})
	common.ApiSuccess(c, operation_setting.GetChannelSchedulerSetting())
}

func validSchedulerRetryJitter(minMillis int, maxMillis int) bool {
	if minMillis == 0 && maxMillis == 0 {
		return true
	}
	return minMillis >= minSchedulerRetryJitterMs &&
		maxMillis <= maxSchedulerRetryJitterMs &&
		minMillis <= maxMillis
}

// GetSchedulerDisabledChannels 当前处于调度器临时禁用状态的渠道列表（Admin）。
func GetSchedulerDisabledChannels(c *gin.Context) {
	views, err := service.ListSchedulerDisabledChannels()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, views)
}

// GetChannelSchedulerChannelConfig 查看某渠道的调度配置与当前禁用状态（Admin）。
func GetChannelSchedulerChannelConfig(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.GetChannelById(channelId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	globalSetting := operation_setting.GetChannelSchedulerSetting()
	common.ApiSuccess(c, gin.H{
		"channel_id":                       channel.Id,
		"channel_name":                     channel.Name,
		"status":                           channel.Status,
		"auto_disabled_until":              channel.AutoDisabledUntil,
		"scheduler_enabled":                channel.SchedulerEnabled,
		"scheduler_retry_times":            channel.SchedulerRetryTimes,
		"scheduler_auto_disable_seconds":   channel.SchedulerAutoDisableSeconds,
		"scheduler_auto_recover_enabled":   channel.SchedulerAutoRecoverEnabled,
		"scheduler_manual_restore_allowed": channel.SchedulerManualRestoreAllowed,
		"effective": gin.H{
			"scheduler_enabled":                channel.GetSchedulerEnabled(),
			"scheduler_retry_times":            channel.ResolveSchedulerRetryTimes(globalSetting.ChannelFailureThreshold),
			"scheduler_auto_disable_seconds":   channel.ResolveSchedulerAutoDisableSeconds(globalSetting.AutoDisableSeconds),
			"scheduler_auto_recover_enabled":   channel.GetSchedulerAutoRecoverEnabled(),
			"scheduler_manual_restore_allowed": channel.GetSchedulerManualRestoreAllowed(),
		},
		"global": gin.H{
			"channel_failure_threshold": globalSetting.ChannelFailureThreshold,
			"auto_disable_seconds":      globalSetting.AutoDisableSeconds,
		},
	})
}

type channelSchedulerChannelConfigRequest struct {
	SchedulerEnabled              *bool `json:"scheduler_enabled"`
	SchedulerRetryTimes           *int  `json:"scheduler_retry_times"`
	SchedulerAutoDisableSeconds   *int  `json:"scheduler_auto_disable_seconds"`
	SchedulerAutoRecoverEnabled   *bool `json:"scheduler_auto_recover_enabled"`
	SchedulerManualRestoreAllowed *bool `json:"scheduler_manual_restore_allowed"`
}

// UpdateChannelSchedulerChannelConfig 保存某渠道的调度配置（Root）。
// 请求体为全量替换语义：字段为 null 表示清除渠道级配置、回退使用全局默认。
func UpdateChannelSchedulerChannelConfig(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.GetChannelById(channelId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	req := channelSchedulerChannelConfigRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.SchedulerRetryTimes != nil && (*req.SchedulerRetryTimes < minSchedulerRetryTimes || *req.SchedulerRetryTimes > maxSchedulerRetryTimes) {
		common.ApiErrorMsg(c, "渠道级失败阈值必须在 1-100 之间")
		return
	}
	if req.SchedulerAutoDisableSeconds != nil && (*req.SchedulerAutoDisableSeconds < minSchedulerDisableSeconds || *req.SchedulerAutoDisableSeconds > maxSchedulerDisableSeconds) {
		common.ApiErrorMsg(c, "渠道级禁用时长必须在 1 秒到 30 天之间")
		return
	}
	updates := map[string]interface{}{
		"scheduler_enabled":                nil,
		"scheduler_retry_times":            nil,
		"scheduler_auto_disable_seconds":   nil,
		"scheduler_auto_recover_enabled":   nil,
		"scheduler_manual_restore_allowed": nil,
	}
	if req.SchedulerEnabled != nil {
		updates["scheduler_enabled"] = *req.SchedulerEnabled
	}
	if req.SchedulerRetryTimes != nil {
		updates["scheduler_retry_times"] = *req.SchedulerRetryTimes
	}
	if req.SchedulerAutoDisableSeconds != nil {
		updates["scheduler_auto_disable_seconds"] = *req.SchedulerAutoDisableSeconds
	}
	if req.SchedulerAutoRecoverEnabled != nil {
		updates["scheduler_auto_recover_enabled"] = *req.SchedulerAutoRecoverEnabled
	}
	if req.SchedulerManualRestoreAllowed != nil {
		updates["scheduler_manual_restore_allowed"] = *req.SchedulerManualRestoreAllowed
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channelId).Updates(updates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	// 内存缓存中的渠道对象持有调度配置字段，整体重建保证一致
	model.InitChannelCache()
	recordManageAudit(c, schedulerChannelCfgAuditKey, map[string]interface{}{
		"id":   channel.Id,
		"name": channel.Name,
	})
	common.ApiSuccess(c, nil)
}

// RestoreSchedulerChannel 手动恢复某个调度器临时禁用的渠道（Root）。
func RestoreSchedulerChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.ManualRestoreSchedulerChannel(channelId, c.GetInt("id"), c.GetString("username")); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, schedulerRestoreAuditAction, map[string]interface{}{
		"id": channelId,
	})
	common.ApiSuccess(c, nil)
}

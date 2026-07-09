package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// ChannelSchedulerSetting 高级渠道调度器配置。
// 语义与旧的 RetryTimes（重试下标对应优先级层级）不同：
// 高级调度器在同一优先级内让单个渠道连续重试到阈值，达到阈值后临时禁用该渠道，
// 换同优先级其他渠道，同级耗尽后才降级到下一优先级。
//
// Enabled=false 时完全走旧调度逻辑。
type ChannelSchedulerSetting struct {
	Enabled                    bool `json:"enabled"`
	ChannelFailureThreshold    int  `json:"channel_failure_threshold"`
	AutoDisableSeconds         int  `json:"auto_disable_seconds"`
	RetryJitterMinMilliseconds int  `json:"retry_jitter_min_ms"`
	RetryJitterMaxMilliseconds int  `json:"retry_jitter_max_ms"`
	AllowPriorityFallback      bool `json:"allow_priority_fallback"`
	LogEnabled                 bool `json:"log_enabled"`
	RespectAutoBan             bool `json:"respect_auto_ban"`
	RetrySameChannel           bool `json:"retry_same_channel"`
	MaxAttemptsPerRequest      int  `json:"max_attempts_per_request"`
}

const (
	defaultSchedulerFailureThreshold = 3
	defaultSchedulerDisableSeconds   = 7200
	defaultSchedulerMaxAttempts      = 12
	minSchedulerRetryJitterMillis    = 100
	maxSchedulerRetryJitterMillis    = 10000
)

var channelSchedulerSetting = ChannelSchedulerSetting{
	Enabled:                    false,
	ChannelFailureThreshold:    defaultSchedulerFailureThreshold,
	AutoDisableSeconds:         defaultSchedulerDisableSeconds,
	RetryJitterMinMilliseconds: 0,
	RetryJitterMaxMilliseconds: 0,
	AllowPriorityFallback:      true,
	LogEnabled:                 true,
	RespectAutoBan:             true,
	RetrySameChannel:           true,
	MaxAttemptsPerRequest:      defaultSchedulerMaxAttempts,
}

func init() {
	config.GlobalConfig.Register("channel_scheduler_setting", &channelSchedulerSetting)
}

// GetChannelSchedulerSetting 返回归一化后的调度器配置。
// 非法值（如阈值 <= 0）回退到默认值，避免配置错误导致死循环或永不禁用。
func GetChannelSchedulerSetting() *ChannelSchedulerSetting {
	if channelSchedulerSetting.ChannelFailureThreshold <= 0 {
		channelSchedulerSetting.ChannelFailureThreshold = defaultSchedulerFailureThreshold
	}
	if channelSchedulerSetting.AutoDisableSeconds <= 0 {
		channelSchedulerSetting.AutoDisableSeconds = defaultSchedulerDisableSeconds
	}
	if channelSchedulerSetting.MaxAttemptsPerRequest <= 0 {
		channelSchedulerSetting.MaxAttemptsPerRequest = defaultSchedulerMaxAttempts
	}
	return &channelSchedulerSetting
}

func (s *ChannelSchedulerSetting) RetryJitterRange() (time.Duration, time.Duration) {
	if s == nil {
		return 0, 0
	}
	minMillis := s.RetryJitterMinMilliseconds
	maxMillis := s.RetryJitterMaxMilliseconds
	if minMillis == 0 && maxMillis == 0 {
		return 0, 0
	}
	if minMillis < minSchedulerRetryJitterMillis || maxMillis > maxSchedulerRetryJitterMillis || minMillis > maxMillis {
		return 0, 0
	}
	return time.Duration(minMillis) * time.Millisecond, time.Duration(maxMillis) * time.Millisecond
}

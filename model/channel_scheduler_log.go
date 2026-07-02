package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 调度日志事件类型。
// failure: 单次渠道失败（观察与正式模式均记录，受 log_enabled 控制）
// observe_disable: 达到阈值但未执行禁用（观察模式 / auto_ban 关闭 / 渠道不参与调度）
// auto_disable: 达到阈值并已执行临时禁用
// auto_recover: 临时禁用到期自动恢复
// manual_restore: 管理员手动恢复
const (
	SchedulerEventFailure        = "failure"
	SchedulerEventObserveDisable = "observe_disable"
	SchedulerEventAutoDisable    = "auto_disable"
	SchedulerEventAutoRecover    = "auto_recover"
	SchedulerEventManualRestore  = "manual_restore"
)

// ChannelSchedulerLog 独立调度日志。
// 第一版落在主库 DB（与渠道表同库，便于关联），不放 LOG_DB，
// 避免 ClickHouse 建表 / TTL / 查询适配成本（见二次开发计划第 10 节）。
type ChannelSchedulerLog struct {
	Id                     int    `json:"id"`
	CreatedAt              int64  `json:"created_at" gorm:"bigint;index:idx_csl_created_at_id,priority:1"`
	EventType              string `json:"event_type" gorm:"type:varchar(32);index;default:''"`
	RequestId              string `json:"request_id" gorm:"type:varchar(64);index;default:''"`
	UserId                 int    `json:"user_id" gorm:"index;default:0"`
	Username               string `json:"username" gorm:"type:varchar(64);default:''"`
	TokenId                int    `json:"token_id" gorm:"default:0"`
	TokenName              string `json:"token_name" gorm:"type:varchar(128);default:''"`
	Group                  string `json:"group" gorm:"type:varchar(64);default:''"`
	ModelName              string `json:"model_name" gorm:"type:varchar(255);index;default:''"`
	ChannelId              int    `json:"channel_id" gorm:"index:idx_csl_channel_id_id,priority:1"`
	ChannelName            string `json:"channel_name" gorm:"default:''"`
	ChannelType            int    `json:"channel_type" gorm:"default:0"`
	Priority               int64  `json:"priority" gorm:"bigint;default:0"`
	AttemptCount           int    `json:"attempt_count" gorm:"default:0"`
	DisableDurationSeconds int    `json:"disable_duration_seconds" gorm:"default:0"`
	DisabledUntil          int64  `json:"disabled_until" gorm:"bigint;default:0"`
	StatusCode             int    `json:"status_code" gorm:"default:0"`
	ErrorCode              string `json:"error_code" gorm:"type:varchar(128);default:''"`
	ErrorType              string `json:"error_type" gorm:"type:varchar(64);default:''"`
	Reason                 string `json:"reason" gorm:"type:text"`
	UsedChannels           string `json:"used_channels" gorm:"type:text"`
	Metadata               string `json:"metadata" gorm:"type:text"`
}

func (ChannelSchedulerLog) TableName() string {
	return "channel_scheduler_logs"
}

// RecordChannelSchedulerLog 写入一条调度日志。写日志失败只记录系统日志，
// 不向调用方传播错误，避免影响转发主链路。
func RecordChannelSchedulerLog(entry *ChannelSchedulerLog) {
	if entry == nil {
		return
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = common.GetTimestamp()
	}
	if err := DB.Create(entry).Error; err != nil {
		common.SysError(fmt.Sprintf("failed to record channel scheduler log: channel_id=%d, event=%s, error=%v", entry.ChannelId, entry.EventType, err))
	}
}

type SchedulerLogQueryParams struct {
	EventType      string
	RequestId      string
	ChannelId      int
	ModelName      string
	Group          string
	Priority       *int64
	StartTimestamp int64
	EndTimestamp   int64
	StartIdx       int
	Num            int
}

func GetChannelSchedulerLogs(params SchedulerLogQueryParams) ([]*ChannelSchedulerLog, int64, error) {
	tx := DB.Model(&ChannelSchedulerLog{})
	if params.EventType != "" {
		tx = tx.Where("event_type = ?", params.EventType)
	}
	if params.RequestId != "" {
		tx = tx.Where("request_id = ?", params.RequestId)
	}
	if params.ChannelId != 0 {
		tx = tx.Where("channel_id = ?", params.ChannelId)
	}
	if params.ModelName != "" {
		tx = tx.Where("model_name = ?", params.ModelName)
	}
	if params.Group != "" {
		tx = tx.Where(commonGroupCol+" = ?", params.Group)
	}
	if params.Priority != nil {
		tx = tx.Where("priority = ?", *params.Priority)
	}
	if params.StartTimestamp != 0 {
		tx = tx.Where("created_at >= ?", params.StartTimestamp)
	}
	if params.EndTimestamp != 0 {
		tx = tx.Where("created_at <= ?", params.EndTimestamp)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []*ChannelSchedulerLog
	err := tx.Order("created_at desc, id desc").Limit(params.Num).Offset(params.StartIdx).Find(&logs).Error
	return logs, total, err
}

type SchedulerLogStat struct {
	TotalCount          int64                     `json:"total_count"`
	FailureCount        int64                     `json:"failure_count"`
	AutoDisableCount    int64                     `json:"auto_disable_count"`
	ObserveDisableCount int64                     `json:"observe_disable_count"`
	AutoRecoverCount    int64                     `json:"auto_recover_count"`
	ManualRestoreCount  int64                     `json:"manual_restore_count"`
	ChannelStats        []SchedulerLogChannelStat `json:"channel_stats"`
}

type SchedulerLogChannelStat struct {
	ChannelId    int    `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	FailureCount int64  `json:"failure_count"`
	DisableCount int64  `json:"disable_count"`
}

func GetChannelSchedulerLogStat(startTimestamp int64, endTimestamp int64) (*SchedulerLogStat, error) {
	stat := &SchedulerLogStat{}
	base := func() *gorm.DB {
		tx := DB.Model(&ChannelSchedulerLog{})
		if startTimestamp != 0 {
			tx = tx.Where("created_at >= ?", startTimestamp)
		}
		if endTimestamp != 0 {
			tx = tx.Where("created_at <= ?", endTimestamp)
		}
		return tx
	}
	if err := base().Count(&stat.TotalCount).Error; err != nil {
		return nil, err
	}
	type eventCount struct {
		EventType string `gorm:"column:event_type"`
		Count     int64  `gorm:"column:count"`
	}
	var eventCounts []eventCount
	if err := base().Select("event_type, count(*) as count").Group("event_type").Find(&eventCounts).Error; err != nil {
		return nil, err
	}
	for _, ec := range eventCounts {
		switch ec.EventType {
		case SchedulerEventFailure:
			stat.FailureCount = ec.Count
		case SchedulerEventAutoDisable:
			stat.AutoDisableCount = ec.Count
		case SchedulerEventObserveDisable:
			stat.ObserveDisableCount = ec.Count
		case SchedulerEventAutoRecover:
			stat.AutoRecoverCount = ec.Count
		case SchedulerEventManualRestore:
			stat.ManualRestoreCount = ec.Count
		}
	}
	type channelRow struct {
		ChannelId    int    `gorm:"column:channel_id"`
		ChannelName  string `gorm:"column:channel_name"`
		FailureCount int64  `gorm:"column:failure_count"`
		DisableCount int64  `gorm:"column:disable_count"`
	}
	var rows []channelRow
	err := base().
		Select("channel_id, max(channel_name) as channel_name, "+
			"sum(case when event_type = ? then 1 else 0 end) as failure_count, "+
			"sum(case when event_type = ? then 1 else 0 end) as disable_count",
			SchedulerEventFailure, SchedulerEventAutoDisable).
		Where("channel_id != 0").
		Group("channel_id").
		Order("failure_count desc").
		Limit(50).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		stat.ChannelStats = append(stat.ChannelStats, SchedulerLogChannelStat{
			ChannelId:    row.ChannelId,
			ChannelName:  row.ChannelName,
			FailureCount: row.FailureCount,
			DisableCount: row.DisableCount,
		})
	}
	return stat, nil
}

// DeleteChannelSchedulerLogs 删除指定时间之前的调度日志，返回删除条数。
func DeleteChannelSchedulerLogs(targetTimestamp int64) (int64, error) {
	result := DB.Where("created_at < ?", targetTimestamp).Delete(&ChannelSchedulerLog{})
	return result.RowsAffected, result.Error
}

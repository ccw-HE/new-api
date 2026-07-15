package model

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ChannelPriorityBucket 同一优先级的候选渠道分桶，按 Priority 从高到低排列。
type ChannelPriorityBucket struct {
	Priority int64      `json:"priority"`
	Channels []*Channel `json:"channels"`
}

// GetSatisfiedChannelBuckets 返回按优先级从高到低分桶的候选渠道列表。
// 内存缓存开启时走缓存路径，关闭时走 abilities 直查路径，两条路径返回顺序一致：
// priority DESC，同 priority 内保持权重选择所需的渠道集合。
// Advanced Custom 渠道依赖 requestPath 过滤，两条路径均保留该过滤。
func GetSatisfiedChannelBuckets(group string, model string, requestPath string) ([]*ChannelPriorityBucket, error) {
	var channels []*Channel
	var err error
	if common.MemoryCacheEnabled {
		channels, err = getSatisfiedChannelsFromCache(group, model, requestPath)
	} else {
		channels, err = getSatisfiedChannelsFromDB(group, model, requestPath)
	}
	if err != nil {
		return nil, err
	}
	return buildPriorityBuckets(channels), nil
}

// buildPriorityBuckets 把候选渠道按优先级降序分桶。
// 显式排序而不依赖调用方顺序，保证缓存与直查两条路径结果一致。
func buildPriorityBuckets(channels []*Channel) []*ChannelPriorityBucket {
	if len(channels) == 0 {
		return nil
	}
	sorted := make([]*Channel, len(channels))
	copy(sorted, channels)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].GetPriority() > sorted[j].GetPriority()
	})
	var buckets []*ChannelPriorityBucket
	for _, channel := range sorted {
		priority := channel.GetPriority()
		if len(buckets) == 0 || buckets[len(buckets)-1].Priority != priority {
			buckets = append(buckets, &ChannelPriorityBucket{Priority: priority})
		}
		bucket := buckets[len(buckets)-1]
		bucket.Channels = append(bucket.Channels, channel)
	}
	return buckets
}

// SetChannelAutoDisabledUntil 更新渠道临时禁用到期时间（DB + 内存缓存）。
func SetChannelAutoDisabledUntil(channelId int, until int64) error {
	err := DB.Model(&Channel{}).Where("id = ?", channelId).Update("auto_disabled_until", until).Error
	if err != nil {
		common.SysError(fmt.Sprintf("failed to update auto_disabled_until: channel_id=%d, error=%v", channelId, err))
		return err
	}
	CacheSetChannelAutoDisabledUntil(channelId, until)
	return nil
}

// SchedulerTempDisableChannel 原子地把处于启用状态的渠道置为调度器临时禁用：
// status、auto_disabled_until、other_info 与 ability.enabled 在同一数据库事务内写入，
// 不存在"status=3 但 until=0"的中间态。前置条件为当前 status=启用（CAS 语义），
// 因此并发的手动禁用、旧式自动禁用或另一个会话的临时禁用都不会被覆盖。
func SchedulerTempDisableChannel(channelId int, reason string, until int64) bool {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}
	changed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := tx.Select("id", "other_info").Where("id = ? AND status = ?", channelId, common.ChannelStatusEnabled).Take(&channel).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		oldOtherInfo := channel.OtherInfo
		info := channel.GetOtherInfo()
		info["status_reason"] = reason
		info["status_time"] = common.GetTimestamp()
		channel.SetOtherInfo(info)
		query := tx.Model(&Channel{}).Where("id = ? AND status = ?", channelId, common.ChannelStatusEnabled)
		if oldOtherInfo == "" {
			query = query.Where("(other_info = ? OR other_info IS NULL)", "")
		} else {
			query = query.Where("other_info = ?", oldOtherInfo)
		}
		result := query.
			Updates(map[string]interface{}{
				"status":              common.ChannelStatusAutoDisabled,
				"auto_disabled_until": until,
				"other_info":          channel.OtherInfo,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		if err := tx.Model(&Ability{}).Where("channel_id = ?", channelId).Update("enabled", false).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		common.SysError(fmt.Sprintf("failed to temp disable channel: channel_id=%d, error=%v", channelId, err))
		return false
	}
	if !changed {
		return false
	}
	if common.MemoryCacheEnabled {
		CacheUpdateChannelStatus(channelId, common.ChannelStatusAutoDisabled)
		CacheSetChannelAutoDisabledUntil(channelId, until)
	}
	return true
}

// SchedulerRecoverChannel 原子地恢复调度器临时禁用的渠道。
// 仅当 status=auto_disabled、auto_disabled_until 与调用方扫描值完全一致且已经到期时执行；
// 数据库 CAS 会拒绝旧恢复任务，避免覆盖并发会话刚写入的新一轮禁用。
func SchedulerRecoverChannel(channelId int, expectedUntil int64) bool {
	if expectedUntil <= 0 || expectedUntil > common.GetTimestamp() {
		return false
	}
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}
	changed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Channel{}).
			Where(
				"id = ? AND status = ? AND auto_disabled_until = ? AND auto_disabled_until <= ?",
				channelId,
				common.ChannelStatusAutoDisabled,
				expectedUntil,
				common.GetTimestamp(),
			).
			Updates(map[string]interface{}{
				"status":              common.ChannelStatusEnabled,
				"auto_disabled_until": 0,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		if err := tx.Model(&Ability{}).Where("channel_id = ?", channelId).Update("enabled", true).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		common.SysError(fmt.Sprintf("failed to recover channel: channel_id=%d, error=%v", channelId, err))
		return false
	}
	if !changed {
		return false
	}
	if common.MemoryCacheEnabled {
		CacheUpdateChannelStatus(channelId, common.ChannelStatusEnabled)
		CacheSetChannelAutoDisabledUntil(channelId, 0)
	}
	return true
}

type AdminChannelStatusUpdate struct {
	Channel                   Channel
	PreviousStatus            int
	PreviousAutoDisabledUntil int64
}

func updateChannelStatusByAdminInTx(tx *gorm.DB, channel *Channel, status int, reason string) (bool, error) {
	if channel.Status == status && channel.AutoDisabledUntil == 0 {
		return false, nil
	}
	previousStatus := channel.Status
	previousUntil := channel.AutoDisabledUntil
	previousOtherInfo := channel.OtherInfo
	info := channel.GetOtherInfo()
	info["status_reason"] = reason
	info["status_time"] = common.GetTimestamp()
	channel.SetOtherInfo(info)

	query := tx.Model(&Channel{}).
		Where("id = ? AND status = ? AND auto_disabled_until = ?", channel.Id, previousStatus, previousUntil)
	if previousOtherInfo == "" {
		query = query.Where("(other_info = ? OR other_info IS NULL)", "")
	} else {
		query = query.Where("other_info = ?", previousOtherInfo)
	}
	result := query.Updates(map[string]interface{}{
		"status":              status,
		"auto_disabled_until": 0,
		"other_info":          channel.OtherInfo,
	})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected != 1 {
		return false, gorm.ErrRecordNotFound
	}
	if err := tx.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", status == common.ChannelStatusEnabled).Error; err != nil {
		return false, err
	}
	return true, nil
}

func updateAdminChannelStatusCache(updates []AdminChannelStatusUpdate, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	for _, update := range updates {
		CacheUpdateChannelStatus(update.Channel.Id, status)
		CacheSetChannelAutoDisabledUntil(update.Channel.Id, 0)
	}
}

func UpdateChannelStatusByAdmin(channelId int, status int, reason string) (*AdminChannelStatusUpdate, error) {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}
	var update *AdminChannelStatusUpdate
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := tx.Where("id = ?", channelId).Take(&channel).Error; err != nil {
			return err
		}
		previousStatus := channel.Status
		previousUntil := channel.AutoDisabledUntil
		changed, err := updateChannelStatusByAdminInTx(tx, &channel, status, reason)
		if err != nil || !changed {
			return err
		}
		update = &AdminChannelStatusUpdate{
			Channel:                   channel,
			PreviousStatus:            previousStatus,
			PreviousAutoDisabledUntil: previousUntil,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if update != nil {
		updateAdminChannelStatusCache([]AdminChannelStatusUpdate{*update}, status)
	}
	return update, nil
}

func UpdateChannelStatusesByAdmin(channelIds []int, status int, reason string) ([]AdminChannelStatusUpdate, error) {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}
	uniqueIds := make(map[int]struct{}, len(channelIds))
	for _, channelId := range channelIds {
		uniqueIds[channelId] = struct{}{}
	}
	updates := make([]AdminChannelStatusUpdate, 0, len(uniqueIds))
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channels []Channel
		if err := tx.Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
			return err
		}
		if len(channels) != len(uniqueIds) {
			return gorm.ErrRecordNotFound
		}
		for i := range channels {
			channel := &channels[i]
			previousStatus := channel.Status
			previousUntil := channel.AutoDisabledUntil
			changed, err := updateChannelStatusByAdminInTx(tx, channel, status, reason)
			if err != nil {
				return err
			}
			if changed {
				updates = append(updates, AdminChannelStatusUpdate{
					Channel:                   *channel,
					PreviousStatus:            previousStatus,
					PreviousAutoDisabledUntil: previousUntil,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	updateAdminChannelStatusCache(updates, status)
	return updates, nil
}

func EnableChannelsByTagByAdmin(tag string, reason string) ([]AdminChannelStatusUpdate, error) {
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}
	updates := make([]AdminChannelStatusUpdate, 0)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channels []Channel
		if err := tx.Where("tag = ?", tag).Find(&channels).Error; err != nil {
			return err
		}
		for i := range channels {
			channel := &channels[i]
			previousStatus := channel.Status
			previousUntil := channel.AutoDisabledUntil
			changed, err := updateChannelStatusByAdminInTx(tx, channel, common.ChannelStatusEnabled, reason)
			if err != nil {
				return err
			}
			if changed {
				updates = append(updates, AdminChannelStatusUpdate{
					Channel:                   *channel,
					PreviousStatus:            previousStatus,
					PreviousAutoDisabledUntil: previousUntil,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	updateAdminChannelStatusCache(updates, common.ChannelStatusEnabled)
	return updates, nil
}

// GetSchedulerTempDisabledChannels 当前处于调度器临时禁用状态的渠道
// （status=auto_disabled 且 auto_disabled_until > 0）。
func GetSchedulerTempDisabledChannels() ([]*Channel, error) {
	var channels []*Channel
	err := DB.Omit("key").
		Where("status = ? AND auto_disabled_until > 0", common.ChannelStatusAutoDisabled).
		Order("auto_disabled_until asc").
		Find(&channels).Error
	return channels, err
}

// HasSchedulerTempDisabledChannels 是否存在可由恢复任务处理的调度器临时禁用渠道。
// 供自动恢复任务的 Enabled 判定使用：即使调度器总开关已关闭，
// 存量临时禁用渠道也必须能够到期恢复；未到期或关闭自动恢复的渠道不唤醒任务。
func HasSchedulerTempDisabledChannels() bool {
	var count int64
	now := common.GetTimestamp()
	err := DB.Model(&Channel{}).
		Where(
			"status = ? AND auto_disabled_until > 0 AND auto_disabled_until <= ? AND (scheduler_auto_recover_enabled IS NULL OR scheduler_auto_recover_enabled = ?)",
			common.ChannelStatusAutoDisabled,
			now,
			true,
		).
		Limit(1).
		Count(&count).Error
	if err != nil {
		common.SysError(fmt.Sprintf("failed to check scheduler temp disabled channels: %v", err))
		return false
	}
	return count > 0
}

// GetExpiredSchedulerDisabledChannels 临时禁用已到期、等待恢复判定的渠道。
// 只返回调度器禁用的渠道（auto_disabled_until > 0），不包含手动禁用渠道
// 和旧式无到期时间的 auto disabled 渠道。
func GetExpiredSchedulerDisabledChannels(now int64) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Omit("key").
		Where("status = ? AND auto_disabled_until > 0 AND auto_disabled_until <= ?", common.ChannelStatusAutoDisabled, now).
		Find(&channels).Error
	return channels, err
}

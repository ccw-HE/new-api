package model

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
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

// HasSchedulerTempDisabledChannels 是否存在调度器临时禁用的渠道。
// 供自动恢复任务的 Enabled 判定使用：即使调度器总开关已关闭，
// 存量临时禁用渠道也必须能够到期恢复。
func HasSchedulerTempDisabledChannels() bool {
	var count int64
	err := DB.Model(&Channel{}).
		Where("status = ? AND auto_disabled_until > 0", common.ChannelStatusAutoDisabled).
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

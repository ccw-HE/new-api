package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetChannelSchedulerLogs 分页查看调度日志（Admin）。
// 支持筛选：event_type、request_id、channel_id、model_name、group、priority、时间范围。
func GetChannelSchedulerLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	params := model.SchedulerLogQueryParams{
		EventType: c.Query("event_type"),
		RequestId: c.Query("request_id"),
		ModelName: c.Query("model_name"),
		Group:     c.Query("group"),
		StartIdx:  pageInfo.GetStartIdx(),
		Num:       pageInfo.GetPageSize(),
	}
	if channelId, err := strconv.Atoi(c.Query("channel_id")); err == nil && channelId != 0 {
		params.ChannelId = channelId
	}
	if priorityStr := c.Query("priority"); priorityStr != "" {
		if priority, err := strconv.ParseInt(priorityStr, 10, 64); err == nil {
			params.Priority = &priority
		}
	}
	if startTimestamp, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64); err == nil {
		params.StartTimestamp = startTimestamp
	}
	if endTimestamp, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64); err == nil {
		params.EndTimestamp = endTimestamp
	}
	logs, total, err := model.GetChannelSchedulerLogs(params)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// GetChannelSchedulerLogsStat 调度日志统计（Admin）：
// 各事件数量与渠道维度的失败/禁用次数排行。
func GetChannelSchedulerLogsStat(c *gin.Context) {
	var startTimestamp, endTimestamp int64
	if value, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64); err == nil {
		startTimestamp = value
	}
	if value, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64); err == nil {
		endTimestamp = value
	}
	stat, err := model.GetChannelSchedulerLogStat(startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, stat)
}

// DeleteChannelSchedulerLogs 清理指定时间之前的调度日志（Root）。
func DeleteChannelSchedulerLogs(c *gin.Context) {
	targetTimestamp, err := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if err != nil || targetTimestamp <= 0 {
		common.ApiErrorMsg(c, "target_timestamp 参数无效")
		return
	}
	count, err := model.DeleteChannelSchedulerLogs(targetTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "scheduler.logs_cleanup", map[string]interface{}{
		"count": count,
	})
	common.ApiSuccess(c, count)
}

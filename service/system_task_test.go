package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSystemTaskRegistry swaps the package registry for the given handlers for
// the duration of a test and restores the original registry afterward.
func withSystemTaskRegistry(t *testing.T, handlers ...SystemTaskHandler) {
	t.Helper()
	systemTaskHandlersMu.Lock()
	saved := systemTaskHandlers
	systemTaskHandlers = map[string]SystemTaskHandler{}
	for _, h := range handlers {
		systemTaskHandlers[h.Type()] = h
	}
	systemTaskHandlersMu.Unlock()
	t.Cleanup(func() {
		systemTaskHandlersMu.Lock()
		systemTaskHandlers = saved
		systemTaskHandlersMu.Unlock()
	})
}

type stubScheduledHandler struct {
	taskType string
	enabled  bool
	interval time.Duration
	onRun    func(ctx context.Context, task *model.SystemTask, runnerID string)
}

type stubSystemTaskRunResult struct {
	taskID   string
	taskType string
	err      error
}

func (h *stubScheduledHandler) Type() string { return h.taskType }

func (h *stubScheduledHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	if h.onRun != nil {
		h.onRun(ctx, task, runnerID)
	}
}

func (h *stubScheduledHandler) Enabled() bool           { return h.enabled }
func (h *stubScheduledHandler) Interval() time.Duration { return h.interval }
func (h *stubScheduledHandler) NewPayload() any         { return nil }

func countSystemTasks(t *testing.T, taskType string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("type = ?", taskType).Count(&count).Error)
	return count
}

func TestSystemTaskSchedulerCreatesWhenDueAndDedups(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_scheduled", enabled: true, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// An active (pending) row already exists, so a second pass must not create
	// another row.
	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Finish the run; with a fresh updated_at the next run is not due yet.
	latest, err := model.GetLatestSystemTask(handler.taskType)
	require.NoError(t, err)
	require.NotNil(t, latest)
	_, claimed, err := model.ClaimSystemTask(latest.ID, handler.taskType, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(latest.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	runSystemTaskScheduler()
	require.Equal(t, int64(1), countSystemTasks(t, handler.taskType))

	// Backdate the finished row beyond the interval -> the job becomes due again.
	require.NoError(t, model.DB.Model(&model.SystemTask{}).
		Where("task_id = ?", latest.TaskID).
		Update("updated_at", common.GetTimestamp()-120).Error)

	runSystemTaskScheduler()
	require.Equal(t, int64(2), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskSchedulerSkipsDisabled(t *testing.T) {
	truncate(t)

	handler := &stubScheduledHandler{taskType: "test_disabled", enabled: false, interval: time.Minute}
	withSystemTaskRegistry(t, handler)

	runSystemTaskScheduler()
	assert.Equal(t, int64(0), countSystemTasks(t, handler.taskType))
}

func TestSystemTaskClaimPassDispatchesByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 1)
	handler := &stubScheduledHandler{
		taskType: "test_dispatch",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskType: task.Type,
				err:      model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handler)

	_, err := model.CreateSystemTask(handler.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	select {
	case got := <-ran:
		require.NoError(t, got.err)
		assert.Equal(t, handler.taskType, got.taskType)
	case <-time.After(2 * time.Second):
		t.Fatal("claimed task was not dispatched to its handler")
	}

	require.Eventually(t, func() bool {
		latest, err := model.GetLatestSystemTask(handler.taskType)
		return err == nil && latest != nil && latest.Status == model.SystemTaskStatusSucceeded
	}, 2*time.Second, 20*time.Millisecond)
}

func TestSystemTaskClaimPassDispatchesEarliestPendingByType(t *testing.T) {
	truncate(t)

	ran := make(chan stubSystemTaskRunResult, 2)
	handlerA := &stubScheduledHandler{
		taskType: "test_dispatch_a",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	handlerB := &stubScheduledHandler{
		taskType: "test_dispatch_b",
		enabled:  true,
		interval: time.Minute,
		onRun: func(_ context.Context, task *model.SystemTask, runnerID string) {
			ran <- stubSystemTaskRunResult{
				taskID: task.TaskID,
				err:    model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, nil, ""),
			}
		},
	}
	withSystemTaskRegistry(t, handlerA, handlerB)

	firstA, err := model.CreateSystemTask(handlerA.taskType, nil, nil)
	require.NoError(t, err)
	secondTaskID, err := model.GenerateSystemTaskID()
	require.NoError(t, err)
	secondA := &model.SystemTask{
		TaskID: secondTaskID,
		Type:   handlerA.taskType,
		Status: model.SystemTaskStatusPending,
	}
	require.NoError(t, model.DB.Create(secondA).Error)
	firstB, err := model.CreateSystemTask(handlerB.taskType, nil, nil)
	require.NoError(t, err)

	runSystemTaskClaimPass("runner-dispatch")

	got := map[string]bool{}
	for range 2 {
		select {
		case result := <-ran:
			require.NoError(t, result.err)
			got[result.taskID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("claimed tasks were not dispatched to their handlers")
		}
	}

	assert.True(t, got[firstA.TaskID])
	assert.True(t, got[firstB.TaskID])
	assert.False(t, got[secondA.TaskID])

	require.Eventually(t, func() bool {
		reloaded, err := model.GetSystemTaskByTaskID(secondA.TaskID)
		return err == nil && reloaded != nil && reloaded.Status == model.SystemTaskStatusPending
	}, 2*time.Second, 20*time.Millisecond)
}

func TestEnqueueSystemTaskReportsCreatedAndExistingActive(t *testing.T) {
	truncate(t)

	first, created, err := EnqueueSystemTask("test_enqueue", map[string]bool{"manual": true})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first)

	existing, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.False(t, created)
	require.NotNil(t, existing)
	assert.Equal(t, first.TaskID, existing.TaskID)

	_, claimed, err := model.ClaimSystemTask(first.ID, first.Type, "runner-a", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, model.FinishSystemTask(first.TaskID, "runner-a", model.SystemTaskStatusSucceeded, nil, ""))

	second, created, err := EnqueueSystemTask("test_enqueue", nil)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, second)
	assert.NotEqual(t, first.TaskID, second.TaskID)
}

func TestNormalizeLogCleanupType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{name: "empty defaults to usage", input: "", expected: logCleanupTypeUsage},
		{name: "usage", input: "usage", expected: logCleanupTypeUsage},
		{name: "scheduler", input: "scheduler", expected: logCleanupTypeScheduler},
		{name: "unknown", input: "server", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := normalizeLogCleanupType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestStartLogCleanupTaskRejectsDifferentActiveLogType(t *testing.T) {
	truncate(t)

	task, err := StartLogCleanupTask(200, logCleanupTypeUsage)
	require.NoError(t, err)
	require.NotNil(t, task)

	sameTask, err := StartLogCleanupTask(200, logCleanupTypeUsage)
	require.NoError(t, err)
	require.NotNil(t, sameTask)
	assert.Equal(t, task.TaskID, sameTask.TaskID)

	differentTask, err := StartLogCleanupTask(200, logCleanupTypeScheduler)
	require.Error(t, err)
	assert.Nil(t, differentTask)
	assert.Contains(t, err.Error(), "different log cleanup task")
}

func TestUsageLogCleanupDoesNotDeleteSchedulerLogs(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	require.NoError(t, model.LOG_DB.Create(&model.Log{
		CreatedAt: 100,
		Type:      model.LogTypeConsume,
		Content:   "usage old",
	}).Error)
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		CreatedAt: 300,
		Type:      model.LogTypeConsume,
		Content:   "usage new",
	}).Error)
	require.NoError(t, model.DB.Create(&model.ChannelSchedulerLog{
		CreatedAt: 100,
		EventType: model.SchedulerEventFailure,
		ChannelId: 1,
	}).Error)

	remaining, err := countLogCleanupRows(ctx, logCleanupTypeUsage, 200)
	require.NoError(t, err)
	assert.EqualValues(t, 1, remaining)

	deleted, err := deleteLogCleanupBatch(ctx, logCleanupTypeUsage, 200, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	var usageCount int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&usageCount).Error)
	assert.EqualValues(t, 1, usageCount)

	var schedulerCount int64
	require.NoError(t, model.DB.Model(&model.ChannelSchedulerLog{}).Count(&schedulerCount).Error)
	assert.EqualValues(t, 1, schedulerCount)
}

func TestSchedulerLogCleanupDoesNotDeleteUsageLogs(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	require.NoError(t, model.LOG_DB.Create(&model.Log{
		CreatedAt: 100,
		Type:      model.LogTypeConsume,
		Content:   "usage old",
	}).Error)
	require.NoError(t, model.DB.Create(&model.ChannelSchedulerLog{
		CreatedAt: 100,
		EventType: model.SchedulerEventFailure,
		ChannelId: 1,
	}).Error)
	require.NoError(t, model.DB.Create(&model.ChannelSchedulerLog{
		CreatedAt: 300,
		EventType: model.SchedulerEventFailure,
		ChannelId: 2,
	}).Error)

	remaining, err := countLogCleanupRows(ctx, logCleanupTypeScheduler, 200)
	require.NoError(t, err)
	assert.EqualValues(t, 1, remaining)

	deleted, err := deleteLogCleanupBatch(ctx, logCleanupTypeScheduler, 200, 100)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	var usageCount int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&usageCount).Error)
	assert.EqualValues(t, 1, usageCount)

	var schedulerCount int64
	require.NoError(t, model.DB.Model(&model.ChannelSchedulerLog{}).Count(&schedulerCount).Error)
	assert.EqualValues(t, 1, schedulerCount)
}

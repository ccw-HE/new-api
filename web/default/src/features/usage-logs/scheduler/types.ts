/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

// ============================================================================
// Channel Scheduler Types (advanced channel scheduler feature)
// ============================================================================

export type SchedulerEventType =
  | 'failure'
  | 'observe_disable'
  | 'auto_disable'
  | 'auto_recover'
  | 'manual_restore'

export interface SchedulerLog {
  id: number
  created_at: number
  event_type: SchedulerEventType
  request_id: string
  user_id: number
  username: string
  token_id: number
  token_name: string
  group: string
  model_name: string
  channel_id: number
  channel_name: string
  channel_type: number
  priority: number
  attempt_count: number
  disable_duration_seconds: number
  disabled_until: number
  status_code: number
  error_code: string
  error_type: string
  reason: string
  used_channels: string
  metadata: string
}

export interface GetSchedulerLogsParams {
  p?: number
  page_size?: number
  event_type?: string
  request_id?: string
  channel_id?: number
  model_name?: string
  group?: string
  priority?: number
  start_timestamp?: number
  end_timestamp?: number
}

export interface GetSchedulerLogsResponse {
  success: boolean
  message?: string
  data?: {
    items: SchedulerLog[]
    total: number
    page: number
    page_size: number
  }
}

export interface SchedulerLogStat {
  total_count: number
  failure_count: number
  auto_disable_count: number
  observe_disable_count: number
  auto_recover_count: number
  manual_restore_count: number
  channel_stats: Array<{
    channel_id: number
    channel_name: string
    failure_count: number
    disable_count: number
  }> | null
}

export interface SchedulerDisabledChannel {
  id: number
  name: string
  type: number
  priority: number
  auto_disabled_until: number
  status_reason: string
  status_time: number
  scheduler_auto_recover_enabled: boolean
  manual_restore_allowed: boolean
}

export interface SchedulerGlobalConfig {
  enabled: boolean
  channel_failure_threshold: number
  auto_disable_seconds: number
  retry_jitter_min_ms: number
  retry_jitter_max_ms: number
  allow_priority_fallback: boolean
  log_enabled: boolean
  respect_auto_ban: boolean
  retry_same_channel: boolean
  max_attempts_per_request: number
  enable_for_stream: boolean
}

export interface SchedulerChannelConfig {
  channel_id: number
  channel_name: string
  status: number
  auto_disabled_until: number
  scheduler_enabled: boolean | null
  scheduler_retry_times: number | null
  scheduler_auto_disable_seconds: number | null
  scheduler_auto_recover_enabled: boolean | null
  scheduler_manual_restore_allowed: boolean | null
  effective: {
    scheduler_enabled: boolean
    scheduler_retry_times: number
    scheduler_auto_disable_seconds: number
    scheduler_auto_recover_enabled: boolean
    scheduler_manual_restore_allowed: boolean
  }
  global: {
    channel_failure_threshold: number
    auto_disable_seconds: number
  }
}

export interface UpdateSchedulerChannelConfigPayload {
  scheduler_enabled: boolean | null
  scheduler_retry_times: number | null
  scheduler_auto_disable_seconds: number | null
  scheduler_auto_recover_enabled: boolean | null
  scheduler_manual_restore_allowed: boolean | null
}

export interface ApiEnvelope<T> {
  success: boolean
  message?: string
  data?: T
}

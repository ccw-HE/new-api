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
import { api } from '@/lib/api'
import { buildQueryParams } from '../lib/utils'
import type {
  ApiEnvelope,
  GetSchedulerLogsParams,
  GetSchedulerLogsResponse,
  SchedulerChannelConfig,
  SchedulerDisabledChannel,
  SchedulerGlobalConfig,
  SchedulerLogStat,
  UpdateSchedulerChannelConfigPayload,
} from './types'

// ============================================================================
// Channel Scheduler APIs (/api/channel_scheduler)
// ============================================================================

export async function getSchedulerLogs(
  params: GetSchedulerLogsParams
): Promise<GetSchedulerLogsResponse> {
  const queryParams = buildQueryParams({
    p: params.p || 1,
    page_size: params.page_size || 20,
    ...params,
  } as unknown as Record<string, unknown>)
  const res = await api.get(`/api/channel_scheduler/logs?${queryParams}`)
  return res.data
}

export async function getSchedulerLogStat(params: {
  start_timestamp?: number
  end_timestamp?: number
}): Promise<ApiEnvelope<SchedulerLogStat>> {
  const queryParams = buildQueryParams(
    params as unknown as Record<string, unknown>
  )
  const res = await api.get(`/api/channel_scheduler/logs/stat?${queryParams}`)
  return res.data
}

export async function getSchedulerDisabledChannels(): Promise<
  ApiEnvelope<SchedulerDisabledChannel[]>
> {
  const res = await api.get('/api/channel_scheduler/disabled')
  return res.data
}

export async function getSchedulerGlobalConfig(): Promise<
  ApiEnvelope<SchedulerGlobalConfig>
> {
  const res = await api.get('/api/channel_scheduler/config')
  return res.data
}

export async function updateSchedulerGlobalConfig(
  config: SchedulerGlobalConfig
): Promise<ApiEnvelope<SchedulerGlobalConfig>> {
  const res = await api.put('/api/channel_scheduler/config', config)
  return res.data
}

export async function getSchedulerChannelConfig(
  channelId: number
): Promise<ApiEnvelope<SchedulerChannelConfig>> {
  const res = await api.get(`/api/channel_scheduler/channel/${channelId}/config`)
  return res.data
}

export async function updateSchedulerChannelConfig(
  channelId: number,
  payload: UpdateSchedulerChannelConfigPayload
): Promise<ApiEnvelope<null>> {
  const res = await api.put(
    `/api/channel_scheduler/channel/${channelId}/config`,
    payload
  )
  return res.data
}

export async function restoreSchedulerChannel(
  channelId: number
): Promise<ApiEnvelope<null>> {
  const res = await api.post(`/api/channel_scheduler/restore/${channelId}`)
  return res.data
}

export const schedulerQueryKeys = {
  all: ['channel-scheduler'] as const,
  logs: (params: Record<string, unknown>) =>
    [...schedulerQueryKeys.all, 'logs', params] as const,
  disabled: () => [...schedulerQueryKeys.all, 'disabled'] as const,
  globalConfig: () => [...schedulerQueryKeys.all, 'global-config'] as const,
  channelConfig: (channelId: number) =>
    [...schedulerQueryKeys.all, 'channel-config', channelId] as const,
}

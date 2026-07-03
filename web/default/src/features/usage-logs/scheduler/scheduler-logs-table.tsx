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
import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { type ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import {
  DataTablePage,
  DataTableRow,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { getSchedulerLogs, schedulerQueryKeys } from './api'
import { getDefaultTimeRange } from '../lib/utils'
import {
  getSchedulerEventConfig,
  useSchedulerLogsColumns,
} from './scheduler-logs-columns'
import { SchedulerLogsFilterBar } from './scheduler-logs-filter-bar'
import type { GetSchedulerLogsParams, SchedulerLog } from './types'

const route = getRouteApi('/_authenticated/usage-logs/$section')

const DEFAULT_SCHEDULER_LOGS_DATA = { items: [] as SchedulerLog[], total: 0 }

function parseUsedChannels(raw: string): string {
  if (!raw) return '-'
  try {
    const parsed = JSON.parse(raw) as unknown
    if (Array.isArray(parsed)) {
      return parsed.map(String).join(' -> ')
    }
  } catch {
    // fall through to raw string
  }
  return raw
}

export function SchedulerLogsTable() {
  const { t } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 640px)')
  const searchParams = route.useSearch()
  const [detailLog, setDetailLog] = useState<SchedulerLog | null>(null)

  const { pagination, onPaginationChange, ensurePageInRange } =
    useTableUrlState({
      search: route.useSearch(),
      navigate: route.useNavigate(),
      pagination: { defaultPage: 1, defaultPageSize: isMobile ? 20 : 100 },
      globalFilter: { enabled: false },
      columnFilters: [],
    })

  const queryParams = useMemo<GetSchedulerLogsParams>(() => {
    const params: GetSchedulerLogsParams = {
      p: pagination.pageIndex + 1,
      page_size: pagination.pageSize,
    }
    if (searchParams.eventType) params.event_type = searchParams.eventType
    if (searchParams.requestId) params.request_id = searchParams.requestId
    if (searchParams.channel) {
      const channelId = Number(searchParams.channel)
      if (!Number.isNaN(channelId) && channelId !== 0) {
        params.channel_id = channelId
      }
    }
    if (searchParams.priority) {
      const priority = Number(searchParams.priority)
      if (!Number.isNaN(priority)) params.priority = priority
    }
    // URL 无时间参数时应用与筛选栏展示一致的默认时间范围
    const defaultRange = getDefaultTimeRange()
    const startTime = searchParams.startTime ?? defaultRange.start.getTime()
    const endTime = searchParams.endTime ?? defaultRange.end.getTime()
    params.start_timestamp = Math.floor(startTime / 1000)
    params.end_timestamp = Math.floor(endTime / 1000)
    return params
  }, [pagination.pageIndex, pagination.pageSize, searchParams])

  const { data, isLoading, isFetching } = useQuery({
    queryKey: schedulerQueryKeys.logs(
      queryParams as unknown as Record<string, unknown>
    ),
    queryFn: async () => {
      const result = await getSchedulerLogs(queryParams)
      if (!result?.success) {
        toast.error(result?.message || t('Failed to load logs'))
        return DEFAULT_SCHEDULER_LOGS_DATA
      }
      return result.data || DEFAULT_SCHEDULER_LOGS_DATA
    },
    placeholderData: (previousData) => previousData,
  })

  const logs = data?.items || []
  const columns = useSchedulerLogsColumns(setDetailLog)

  const { table } = useDataTable({
    data: logs as unknown as Record<string, unknown>[],
    columns: columns as ColumnDef<Record<string, unknown>>[],
    columnFilters: [],
    columnVisibilityStorageKey: 'usage-logs:scheduler:column-visibility',
    pagination,
    enableRowSelection: false,
    onPaginationChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: data?.total || 0,
    ensurePageInRange,
  })

  const detailEventConfig = detailLog
    ? getSchedulerEventConfig(detailLog.event_type)
    : null

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns as ColumnDef<Record<string, unknown>>[]}
        isLoading={isLoading || (isFetching && !data)}
        isFetching={isFetching}
        emptyTitle={t('No Scheduler Logs Found')}
        emptyDescription={t(
          'No channel scheduler logs available. Enable the channel scheduler or its observation mode to start recording.'
        )}
        skeletonKeyPrefix='scheduler-log-skeleton'
        applyHeaderSize
        toolbar={<SchedulerLogsFilterBar table={table} />}
        renderRow={(row) => (
          <DataTableRow
            key={row.id}
            row={row}
            className='transition-colors'
            getColumnClassName={() => 'py-2'}
          />
        )}
      />

      <Dialog
        open={detailLog !== null}
        onOpenChange={(open) => !open && setDetailLog(null)}
        title={t('Scheduler Log Details')}
        contentHeight='auto'
      >
        {detailLog && (
          <div className='space-y-3 py-2 text-sm'>
            <div className='flex items-center gap-2'>
              <StatusBadge
                label={t(detailEventConfig?.label ?? detailLog.event_type)}
                variant={detailEventConfig?.variant ?? 'neutral'}
                size='sm'
                copyable={false}
              />
              <span className='text-muted-foreground font-mono text-xs'>
                {formatTimestampToDate(detailLog.created_at)}
              </span>
            </div>
            <div className='grid grid-cols-2 gap-x-4 gap-y-2'>
              <DetailItem
                label={t('Channel')}
                value={`${detailLog.channel_name || '-'} (#${detailLog.channel_id})`}
              />
              <DetailItem
                label={t('Model')}
                value={detailLog.model_name || '-'}
              />
              <DetailItem label={t('Group')} value={detailLog.group || '-'} />
              <DetailItem
                label={t('Priority')}
                value={String(detailLog.priority)}
              />
              <DetailItem
                label={t('Attempts')}
                value={String(detailLog.attempt_count || '-')}
              />
              <DetailItem
                label={t('Status Code')}
                value={String(detailLog.status_code || '-')}
              />
              <DetailItem
                label={t('Error Code')}
                value={detailLog.error_code || '-'}
              />
              <DetailItem
                label={t('Error Type')}
                value={detailLog.error_type || '-'}
              />
              {detailLog.disable_duration_seconds > 0 && (
                <DetailItem
                  label={t('Disable Duration')}
                  value={`${detailLog.disable_duration_seconds}s`}
                />
              )}
              {detailLog.disabled_until > 0 && (
                <DetailItem
                  label={t('Disabled Until')}
                  value={formatTimestampToDate(detailLog.disabled_until)}
                />
              )}
              <DetailItem
                label={t('Request ID')}
                value={detailLog.request_id || '-'}
                mono
              />
              <DetailItem
                label={t('User')}
                value={
                  detailLog.username
                    ? `${detailLog.username} (#${detailLog.user_id})`
                    : '-'
                }
              />
            </div>
            <DetailItem
              label={t('Channel Path')}
              value={parseUsedChannels(detailLog.used_channels)}
              mono
            />
            {detailLog.reason && (
              <div className='space-y-1'>
                <p className='text-muted-foreground text-xs'>{t('Reason')}</p>
                <p className='bg-muted/50 max-h-40 overflow-auto rounded-md p-2 text-xs break-all whitespace-pre-wrap'>
                  {detailLog.reason}
                </p>
              </div>
            )}
            {detailLog.metadata && (
              <div className='space-y-1'>
                <p className='text-muted-foreground text-xs'>
                  {t('Metadata')}
                </p>
                <p className='bg-muted/50 max-h-32 overflow-auto rounded-md p-2 font-mono text-xs break-all whitespace-pre-wrap'>
                  {detailLog.metadata}
                </p>
              </div>
            )}
          </div>
        )}
      </Dialog>
    </>
  )
}

function DetailItem({
  label,
  value,
  mono,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className='min-w-0'>
      <p className='text-muted-foreground text-xs'>{label}</p>
      <p className={`truncate text-xs ${mono ? 'font-mono' : ''}`} title={value}>
        {value}
      </p>
    </div>
  )
}

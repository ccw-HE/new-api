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
import { type ColumnDef } from '@tanstack/react-table'
import { Eye } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatTimestampToDate } from '@/lib/format'
import { Button } from '@/components/ui/button'
import {
  StatusBadge,
  type StatusBadgeProps,
} from '@/components/status-badge'
import type { SchedulerEventType, SchedulerLog } from './types'

export const SCHEDULER_EVENT_CONFIG: Record<
  SchedulerEventType,
  { label: string; variant: NonNullable<StatusBadgeProps['variant']> }
> = {
  failure: { label: 'Channel Failure', variant: 'danger' },
  observe_disable: { label: 'Would Disable (Observe)', variant: 'warning' },
  auto_disable: { label: 'Auto Disabled', variant: 'danger' },
  auto_recover: { label: 'Auto Recovered', variant: 'success' },
  manual_restore: { label: 'Manually Restored', variant: 'info' },
}

export function getSchedulerEventConfig(eventType: string) {
  return (
    SCHEDULER_EVENT_CONFIG[eventType as SchedulerEventType] ?? {
      label: eventType,
      variant: 'neutral' as const,
    }
  )
}

function formatDurationShort(seconds: number): string {
  if (!seconds || seconds <= 0) return '-'
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

export function useSchedulerLogsColumns(
  onViewDetails: (log: SchedulerLog) => void
): ColumnDef<SchedulerLog>[] {
  const { t } = useTranslation()

  return [
    {
      accessorKey: 'created_at',
      header: t('Time'),
      cell: ({ row }) => {
        const log = row.original
        const config = getSchedulerEventConfig(log.event_type)
        return (
          <div className='flex min-w-0 flex-col gap-0.5'>
            <span className='truncate font-mono text-xs tabular-nums'>
              {formatTimestampToDate(log.created_at)}
            </span>
            <StatusBadge
              label={t(config.label)}
              variant={config.variant}
              size='sm'
              copyable={false}
              className='!text-xs [&_span]:!text-xs'
            />
          </div>
        )
      },
      enableHiding: false,
      size: 190,
    },
    {
      id: 'channel',
      accessorKey: 'channel_id',
      header: t('Channel'),
      cell: ({ row }) => {
        const log = row.original
        return (
          <div className='flex min-w-0 flex-col'>
            <span className='truncate text-xs font-medium'>
              {log.channel_name || '-'}
            </span>
            <span className='text-muted-foreground font-mono text-xs'>
              #{log.channel_id}
            </span>
          </div>
        )
      },
      size: 150,
    },
    {
      accessorKey: 'model_name',
      header: t('Model'),
      cell: ({ row }) => (
        <span className='truncate font-mono text-xs'>
          {row.original.model_name || '-'}
        </span>
      ),
      size: 170,
    },
    {
      accessorKey: 'group',
      header: t('Group'),
      cell: ({ row }) => (
        <span className='truncate text-xs'>{row.original.group || '-'}</span>
      ),
      size: 90,
    },
    {
      accessorKey: 'priority',
      header: t('Priority'),
      cell: ({ row }) => (
        <span className='font-mono text-xs tabular-nums'>
          {row.original.priority}
        </span>
      ),
      size: 70,
    },
    {
      accessorKey: 'attempt_count',
      header: t('Attempts'),
      cell: ({ row }) => (
        <span className='font-mono text-xs tabular-nums'>
          {row.original.attempt_count || '-'}
        </span>
      ),
      size: 80,
    },
    {
      id: 'disable_info',
      header: t('Disable Info'),
      cell: ({ row }) => {
        const log = row.original
        if (!log.disabled_until && !log.disable_duration_seconds) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }
        return (
          <div className='flex min-w-0 flex-col'>
            <span className='text-xs'>
              {formatDurationShort(log.disable_duration_seconds)}
            </span>
            {log.disabled_until > 0 && (
              <span className='text-muted-foreground truncate font-mono text-xs'>
                {t('Until')} {formatTimestampToDate(log.disabled_until)}
              </span>
            )}
          </div>
        )
      },
      size: 170,
    },
    {
      accessorKey: 'status_code',
      header: t('Status Code'),
      cell: ({ row }) => {
        const log = row.original
        return (
          <div className='flex min-w-0 flex-col'>
            <span className='font-mono text-xs tabular-nums'>
              {log.status_code || '-'}
            </span>
            {log.error_code && (
              <span
                className='text-muted-foreground truncate font-mono text-xs'
                title={log.error_code}
              >
                {log.error_code}
              </span>
            )}
          </div>
        )
      },
      size: 130,
    },
    {
      accessorKey: 'reason',
      header: t('Reason'),
      cell: ({ row }) => {
        const reason = row.original.reason
        if (!reason) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }
        return (
          <span className='line-clamp-2 text-xs break-all' title={reason}>
            {reason}
          </span>
        )
      },
      size: 260,
    },
    {
      accessorKey: 'request_id',
      header: t('Request ID'),
      cell: ({ row }) => (
        <span
          className='truncate font-mono text-xs'
          title={row.original.request_id}
        >
          {row.original.request_id || '-'}
        </span>
      ),
      size: 140,
    },
    {
      id: 'actions',
      header: () => t('Actions'),
      cell: ({ row }) => (
        <Button
          variant='ghost'
          size='icon-sm'
          aria-label={t('View Details')}
          onClick={() => onViewDetails(row.original)}
        >
          <Eye className='size-4' />
        </Button>
      ),
      enableSorting: false,
      enableHiding: false,
      size: 60,
      meta: { pinned: 'right' as const },
    },
  ]
}

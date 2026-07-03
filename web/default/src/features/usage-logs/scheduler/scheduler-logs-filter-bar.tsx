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
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useIsFetching, useQueryClient } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { type Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { CompactDateTimeRangePicker } from '../components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '../components/logs-filter-toolbar'
import { getDefaultTimeRange } from '../lib/utils'
import { SCHEDULER_EVENT_CONFIG } from './scheduler-logs-columns'

const route = getRouteApi('/_authenticated/usage-logs/$section')

const EVENT_TYPE_ALL = 'all'

interface SchedulerLogsFilters {
  startTime?: Date
  endTime?: Date
  channel?: string
  requestId?: string
  eventType?: string
  priority?: string
}

interface SchedulerLogsFilterBarProps<TData> {
  table: Table<TData>
}

export function SchedulerLogsFilterBar<TData>({
  table,
}: SchedulerLogsFilterBarProps<TData>) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const searchParams = route.useSearch()
  const fetchingLogs = useIsFetching({ queryKey: ['channel-scheduler'] })

  const [filters, setFilters] = useState<SchedulerLogsFilters>(() => {
    const { start, end } = getDefaultTimeRange()
    return { startTime: start, endTime: end, eventType: EVENT_TYPE_ALL }
  })

  useEffect(() => {
    const { start, end } = getDefaultTimeRange()
    setFilters({
      startTime: searchParams.startTime
        ? new Date(searchParams.startTime)
        : start,
      endTime: searchParams.endTime ? new Date(searchParams.endTime) : end,
      channel: searchParams.channel ? String(searchParams.channel) : '',
      requestId: searchParams.requestId || '',
      eventType: searchParams.eventType || EVENT_TYPE_ALL,
      priority: searchParams.priority || '',
    })
  }, [
    searchParams.startTime,
    searchParams.endTime,
    searchParams.channel,
    searchParams.requestId,
    searchParams.eventType,
    searchParams.priority,
  ])

  const handleChange = useCallback(
    (field: keyof SchedulerLogsFilters, value: Date | string | undefined) => {
      setFilters((prev) => ({ ...prev, [field]: value }))
    },
    []
  )

  const handleApply = useCallback(() => {
    navigate({
      to: '/usage-logs/$section',
      params: { section: 'scheduler' },
      search: {
        page: 1,
        startTime: filters.startTime?.getTime(),
        endTime: filters.endTime?.getTime(),
        channel: filters.channel || undefined,
        requestId: filters.requestId || undefined,
        eventType:
          filters.eventType && filters.eventType !== EVENT_TYPE_ALL
            ? filters.eventType
            : undefined,
        priority: filters.priority || undefined,
      },
    })
    queryClient.invalidateQueries({ queryKey: ['channel-scheduler'] })
  }, [filters, navigate, queryClient])

  const handleReset = useCallback(() => {
    const { start, end } = getDefaultTimeRange()
    setFilters({
      startTime: start,
      endTime: end,
      channel: '',
      requestId: '',
      eventType: EVENT_TYPE_ALL,
      priority: '',
    })
    navigate({
      to: '/usage-logs/$section',
      params: { section: 'scheduler' },
      search: {
        page: 1,
        startTime: start.getTime(),
        endTime: end.getTime(),
      },
    })
    queryClient.invalidateQueries({ queryKey: ['channel-scheduler'] })
  }, [navigate, queryClient])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') handleApply()
    },
    [handleApply]
  )

  const eventTypeItems = useMemo(
    () => [
      { value: EVENT_TYPE_ALL, label: t('All Events') },
      ...Object.entries(SCHEDULER_EVENT_CONFIG).map(([value, config]) => ({
        value,
        label: t(config.label),
      })),
    ],
    [t]
  )
  const eventTypeLabel =
    eventTypeItems.find((item) => item.value === filters.eventType)?.label ??
    t('All Events')

  const hasAdditionalFilters =
    !!filters.channel ||
    !!filters.requestId ||
    !!filters.priority ||
    (filters.eventType && filters.eventType !== EVENT_TYPE_ALL)

  const dateRangeFilter = (
    <LogsFilterField wide>
      <CompactDateTimeRangePicker
        start={filters.startTime}
        end={filters.endTime}
        onChange={({ start, end }) => {
          handleChange('startTime', start)
          handleChange('endTime', end)
        }}
      />
    </LogsFilterField>
  )
  const eventTypeFilter = (
    <LogsFilterField>
      <Select
        items={eventTypeItems}
        value={filters.eventType ?? EVENT_TYPE_ALL}
        onValueChange={(value) => {
          handleChange('eventType', value === null ? EVENT_TYPE_ALL : value)
        }}
      >
        <SelectTrigger aria-label={t('Event Type')}>
          <SelectValue>{eventTypeLabel}</SelectValue>
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          <SelectGroup>
            {eventTypeItems.map((item) => (
              <SelectItem key={item.value} value={item.value}>
                {item.label}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </LogsFilterField>
  )
  const channelFilter = (
    <LogsFilterField>
      <LogsFilterInput
        placeholder={t('Channel ID')}
        value={filters.channel || ''}
        onChange={(e) => handleChange('channel', e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  )
  const requestIdFilter = (
    <LogsFilterField>
      <LogsFilterInput
        placeholder={t('Request ID')}
        value={filters.requestId || ''}
        onChange={(e) => handleChange('requestId', e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  )
  const priorityFilter = (
    <LogsFilterField>
      <LogsFilterInput
        placeholder={t('Priority')}
        value={filters.priority || ''}
        onChange={(e) => handleChange('priority', e.target.value)}
        onKeyDown={handleKeyDown}
      />
    </LogsFilterField>
  )

  return (
    <LogsFilterToolbar
      table={table}
      primaryFilters={
        <>
          {dateRangeFilter}
          {eventTypeFilter}
          {channelFilter}
          {requestIdFilter}
          {priorityFilter}
        </>
      }
      mobilePinnedFilters={dateRangeFilter}
      mobileFilters={
        <>
          {eventTypeFilter}
          {channelFilter}
          {requestIdFilter}
          {priorityFilter}
        </>
      }
      mobileFilterCount={
        [
          filters.channel,
          filters.requestId,
          filters.priority,
          filters.eventType !== EVENT_TYPE_ALL ? filters.eventType : '',
        ].filter(Boolean).length
      }
      hasActiveFilters={!!hasAdditionalFilters}
      onSearch={handleApply}
      searchLoading={fetchingLogs > 0}
      onReset={handleReset}
    />
  )
}

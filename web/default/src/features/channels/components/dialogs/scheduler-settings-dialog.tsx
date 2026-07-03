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
import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Dialog } from '@/components/dialog'
import {
  getSchedulerDisabledChannels,
  getSchedulerGlobalConfig,
  restoreSchedulerChannel,
  schedulerQueryKeys,
  updateSchedulerGlobalConfig,
} from '@/features/usage-logs/scheduler/api'
import type { SchedulerGlobalConfig } from '@/features/usage-logs/scheduler/types'
import { channelsQueryKeys } from '../../lib'

type SchedulerSettingsDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

type PanelTab = 'disabled' | 'config'

export function SchedulerSettingsDialog({
  open,
  onOpenChange,
}: SchedulerSettingsDialogProps) {
  const { t } = useTranslation()
  const isRoot = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.SUPER_ADMIN
  )
  const [tab, setTab] = useState<PanelTab>('disabled')

  useEffect(() => {
    if (!open) setTab('disabled')
  }, [open])

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Channel Scheduler')}
      description={t(
        'Same-priority failover: a channel failing consecutively is temporarily disabled, then same-priority channels are tried before falling back to lower priority.'
      )}
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <div className='space-y-4 py-2'>
        <div className='flex items-center justify-between gap-2'>
          <Tabs value={tab} onValueChange={(value) => setTab(value as PanelTab)}>
            <TabsList>
              <TabsTrigger value='disabled'>
                {t('Temp-Disabled Channels')}
              </TabsTrigger>
              {isRoot && (
                <TabsTrigger value='config'>{t('Global Settings')}</TabsTrigger>
              )}
            </TabsList>
          </Tabs>
          <Button
            variant='link'
            size='sm'
            className='shrink-0 px-0'
            render={
              <Link
                to='/usage-logs/$section'
                params={{ section: 'scheduler' }}
              />
            }
          >
            {t('View Scheduler Logs')}
          </Button>
        </div>

        {tab === 'disabled' ? (
          <DisabledChannelsPanel active={open} isRoot={isRoot} />
        ) : (
          <GlobalConfigPanel active={open && isRoot} />
        )}
      </div>
    </Dialog>
  )
}

function DisabledChannelsPanel({
  active,
  isRoot,
}: {
  active: boolean
  isRoot: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: schedulerQueryKeys.disabled(),
    queryFn: getSchedulerDisabledChannels,
    enabled: active,
    refetchInterval: 30000,
  })
  const channels = data?.data ?? []

  const restoreMutation = useMutation({
    mutationFn: restoreSchedulerChannel,
    onSuccess: (result) => {
      if (result.success) {
        toast.success(t('Channel restored'))
        queryClient.invalidateQueries({ queryKey: schedulerQueryKeys.all })
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
      }
    },
  })

  if (isLoading) {
    return (
      <div className='text-muted-foreground flex items-center gap-2 py-8 text-sm'>
        <Loader2 className='size-4 animate-spin' />
        {t('Loading...')}
      </div>
    )
  }

  if (channels.length === 0) {
    return (
      <p className='text-muted-foreground py-8 text-center text-sm'>
        {t('No channels are currently temporarily disabled by the scheduler.')}
      </p>
    )
  }

  return (
    <div className='max-h-96 overflow-auto rounded-md border'>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Channel')}</TableHead>
            <TableHead>{t('Priority')}</TableHead>
            <TableHead>{t('Reason')}</TableHead>
            <TableHead>{t('Disabled Until')}</TableHead>
            <TableHead>{t('Auto Recover')}</TableHead>
            <TableHead className='text-right'>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {channels.map((channel) => (
            <TableRow key={channel.id}>
              <TableCell>
                <div className='flex min-w-0 flex-col'>
                  <span className='truncate text-xs font-medium'>
                    {channel.name}
                  </span>
                  <span className='text-muted-foreground font-mono text-xs'>
                    #{channel.id}
                  </span>
                </div>
              </TableCell>
              <TableCell className='font-mono text-xs'>
                {channel.priority}
              </TableCell>
              <TableCell className='max-w-52'>
                <span
                  className='line-clamp-2 text-xs break-all'
                  title={channel.status_reason}
                >
                  {channel.status_reason || '-'}
                </span>
              </TableCell>
              <TableCell className='font-mono text-xs'>
                {formatTimestampToDate(channel.auto_disabled_until)}
              </TableCell>
              <TableCell>
                <Badge
                  variant={
                    channel.scheduler_auto_recover_enabled
                      ? 'secondary'
                      : 'outline'
                  }
                  className='text-xs'
                >
                  {channel.scheduler_auto_recover_enabled
                    ? t('Enabled')
                    : t('Disabled')}
                </Badge>
              </TableCell>
              <TableCell className='text-right'>
                {isRoot && channel.manual_restore_allowed ? (
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={restoreMutation.isPending}
                    onClick={() => restoreMutation.mutate(channel.id)}
                  >
                    <RotateCcw className='mr-1 size-3.5' />
                    {t('Restore')}
                  </Button>
                ) : (
                  <span className='text-muted-foreground text-xs'>-</span>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

const SWITCH_FIELDS: Array<{
  key: keyof SchedulerGlobalConfig
  label: string
  description: string
}> = [
  {
    key: 'enabled',
    label: 'Enable Advanced Scheduler',
    description:
      'Master switch. When off, the legacy retry behavior is used unchanged.',
  },
  {
    key: 'observation_only',
    label: 'Observation Mode',
    description:
      'Only record scheduler logs without changing any scheduling behavior. Recommended before enabling.',
  },
  {
    key: 'log_enabled',
    label: 'Scheduler Logging',
    description: 'Write independent scheduler logs for failures and disables.',
  },
  {
    key: 'retry_same_channel',
    label: 'Retry Same Channel',
    description:
      'Keep retrying the same channel until its failure threshold is reached.',
  },
  {
    key: 'allow_priority_fallback',
    label: 'Priority Fallback',
    description:
      'Fall back to lower priority channels after the current priority is exhausted.',
  },
  {
    key: 'respect_auto_ban',
    label: 'Respect Channel Auto-Ban Switch',
    description:
      'Channels with auto-ban off are excluded from this request but never temporarily disabled.',
  },
  {
    key: 'enable_for_stream',
    label: 'Enable for Streaming Requests',
    description:
      'Not recommended for the first rollout: partially streamed responses cannot be retried safely.',
  },
  {
    key: 'enable_for_task_relay',
    label: 'Enable for Task Relay',
    description:
      'Reserved for a future version. Task relays (Midjourney/Suno/video) currently keep the legacy retry behavior.',
  },
]

const NUMBER_FIELDS: Array<{
  key: 'channel_failure_threshold' | 'auto_disable_seconds' | 'max_attempts_per_request'
  label: string
  description: string
  min: number
  max: number
}> = [
  {
    key: 'channel_failure_threshold',
    label: 'Failure Threshold',
    description:
      'Consecutive failures before a channel is temporarily disabled (per-channel override available).',
    min: 1,
    max: 1000,
  },
  {
    key: 'auto_disable_seconds',
    label: 'Auto Disable Seconds',
    description: 'Temporary disable duration in seconds. Default 7200 (2 hours).',
    min: 1,
    max: 2592000,
  },
  {
    key: 'max_attempts_per_request',
    label: 'Max Attempts Per Request',
    description:
      'Hard cap of upstream attempts within one request to bound total latency.',
    min: 1,
    max: 100,
  },
]

function GlobalConfigPanel({ active }: { active: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [config, setConfig] = useState<SchedulerGlobalConfig | null>(null)
  const [numberDrafts, setNumberDrafts] = useState<Record<string, string>>({})

  const { data, isLoading } = useQuery({
    queryKey: schedulerQueryKeys.globalConfig(),
    queryFn: getSchedulerGlobalConfig,
    enabled: active,
  })

  useEffect(() => {
    if (data?.success && data.data) {
      setConfig(data.data)
      setNumberDrafts({
        channel_failure_threshold: String(data.data.channel_failure_threshold),
        auto_disable_seconds: String(data.data.auto_disable_seconds),
        max_attempts_per_request: String(data.data.max_attempts_per_request),
      })
    }
  }, [data])

  const saveMutation = useMutation({
    mutationFn: updateSchedulerGlobalConfig,
    onSuccess: (result) => {
      if (result.success) {
        toast.success(t('Scheduler settings saved'))
        queryClient.invalidateQueries({
          queryKey: schedulerQueryKeys.globalConfig(),
        })
      }
    },
  })

  const handleSave = () => {
    if (!config) return
    const parsed: Record<string, number> = {}
    for (const field of NUMBER_FIELDS) {
      const raw = (numberDrafts[field.key] ?? '').trim()
      const value = Number(raw)
      if (raw === '' || Number.isNaN(value) || value < field.min || value > field.max) {
        toast.error(
          `${t(field.label)}: ${t('must be a number between')} ${field.min} - ${field.max}`
        )
        return
      }
      parsed[field.key] = value
    }
    saveMutation.mutate({
      ...config,
      channel_failure_threshold: parsed.channel_failure_threshold,
      auto_disable_seconds: parsed.auto_disable_seconds,
      max_attempts_per_request: parsed.max_attempts_per_request,
    })
  }

  if (isLoading || !config) {
    return (
      <div className='text-muted-foreground flex items-center gap-2 py-8 text-sm'>
        <Loader2 className='size-4 animate-spin' />
        {t('Loading...')}
      </div>
    )
  }

  return (
    <div className='space-y-4'>
      <div className='max-h-96 space-y-4 overflow-auto pr-1'>
        {SWITCH_FIELDS.map((field) => (
          <div
            key={field.key}
            className='flex items-start justify-between gap-4'
          >
            <div className='min-w-0 space-y-0.5'>
              <Label className='text-sm'>{t(field.label)}</Label>
              <p className='text-muted-foreground text-xs'>
                {t(field.description)}
              </p>
            </div>
            <Switch
              checked={Boolean(config[field.key])}
              onCheckedChange={(checked) =>
                setConfig((prev) =>
                  prev ? { ...prev, [field.key]: checked } : prev
                )
              }
            />
          </div>
        ))}
        {NUMBER_FIELDS.map((field) => (
          <div key={field.key} className='space-y-1'>
            <Label className='text-sm' htmlFor={`scheduler-${field.key}`}>
              {t(field.label)}
            </Label>
            <Input
              id={`scheduler-${field.key}`}
              type='number'
              min={field.min}
              max={field.max}
              value={numberDrafts[field.key] ?? ''}
              onChange={(e) =>
                setNumberDrafts((prev) => ({
                  ...prev,
                  [field.key]: e.target.value,
                }))
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(field.description)}
            </p>
          </div>
        ))}
      </div>
      <div className='flex justify-end'>
        <Button onClick={handleSave} disabled={saveMutation.isPending}>
          {saveMutation.isPending && (
            <Loader2 className='mr-2 size-4 animate-spin' />
          )}
          {t('Save Scheduler Settings')}
        </Button>
      </div>
    </div>
  )
}

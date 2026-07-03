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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Loader2, RotateCcw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  getSchedulerChannelConfig,
  restoreSchedulerChannel,
  schedulerQueryKeys,
  updateSchedulerChannelConfig,
} from '@/features/usage-logs/scheduler/api'
import type { UpdateSchedulerChannelConfigPayload } from '@/features/usage-logs/scheduler/types'
import { formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { CHANNEL_STATUS } from '../../constants'
import { channelsQueryKeys } from '../../lib'
import { useChannels } from '../channels-provider'

type ChannelSchedulerConfigDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface FormState {
  enabled: boolean
  autoRecover: boolean
  manualRestore: boolean
  retryTimes: string
  disableSeconds: string
}

export function ChannelSchedulerConfigDialog({
  open,
  onOpenChange,
}: ChannelSchedulerConfigDialogProps) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const queryClient = useQueryClient()
  const isRoot = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.SUPER_ADMIN
  )
  const [form, setForm] = useState<FormState | null>(null)

  const channelId = currentRow?.id ?? 0

  const { data, isLoading } = useQuery({
    queryKey: schedulerQueryKeys.channelConfig(channelId),
    queryFn: () => getSchedulerChannelConfig(channelId),
    enabled: open && channelId > 0,
  })
  const config = data?.success ? data.data : undefined

  useEffect(() => {
    if (!open) {
      setForm(null)
      return
    }
    if (config) {
      setForm({
        enabled: config.effective.scheduler_enabled,
        autoRecover: config.effective.scheduler_auto_recover_enabled,
        manualRestore: config.effective.scheduler_manual_restore_allowed,
        retryTimes:
          config.scheduler_retry_times != null
            ? String(config.scheduler_retry_times)
            : '',
        disableSeconds:
          config.scheduler_auto_disable_seconds != null
            ? String(config.scheduler_auto_disable_seconds)
            : '',
      })
    }
  }, [open, config])

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: schedulerQueryKeys.all })
    queryClient.invalidateQueries({ queryKey: channelsQueryKeys.all })
  }

  const saveMutation = useMutation({
    mutationFn: (payload: UpdateSchedulerChannelConfigPayload) =>
      updateSchedulerChannelConfig(channelId, payload),
    onSuccess: (result) => {
      if (result.success) {
        toast.success(t('Channel scheduler settings saved'))
        invalidate()
        onOpenChange(false)
      }
    },
  })

  const restoreMutation = useMutation({
    mutationFn: () => restoreSchedulerChannel(channelId),
    onSuccess: (result) => {
      if (result.success) {
        toast.success(t('Channel restored'))
        invalidate()
      }
    },
  })

  if (!currentRow) return null

  const isTempDisabled =
    config != null &&
    config.status === CHANNEL_STATUS.AUTO_DISABLED &&
    config.auto_disabled_until > 0
  const isSchedulerDisableExpired =
    isTempDisabled &&
    config.auto_disabled_until <= Math.floor(Date.now() / 1000)
  const canManualRestore =
    isRoot &&
    isSchedulerDisableExpired &&
    config?.effective.scheduler_manual_restore_allowed

  const handleSave = () => {
    if (!form) return
    const retryTimes = form.retryTimes.trim()
    const disableSeconds = form.disableSeconds.trim()
    if (
      retryTimes !== '' &&
      (Number(retryTimes) < 1 || Number.isNaN(Number(retryTimes)))
    ) {
      toast.error(t('Failure threshold must be a positive number'))
      return
    }
    if (
      disableSeconds !== '' &&
      (Number(disableSeconds) < 1 || Number.isNaN(Number(disableSeconds)))
    ) {
      toast.error(t('Disable duration must be a positive number of seconds'))
      return
    }
    saveMutation.mutate({
      scheduler_enabled: form.enabled,
      scheduler_auto_recover_enabled: form.autoRecover,
      scheduler_manual_restore_allowed: form.manualRestore,
      scheduler_retry_times: retryTimes === '' ? null : Number(retryTimes),
      scheduler_auto_disable_seconds:
        disableSeconds === '' ? null : Number(disableSeconds),
    })
  }

  const handleResetToGlobal = () => {
    saveMutation.mutate({
      scheduler_enabled: null,
      scheduler_auto_recover_enabled: null,
      scheduler_manual_restore_allowed: null,
      scheduler_retry_times: null,
      scheduler_auto_disable_seconds: null,
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Channel Scheduler Settings')}
      description={
        <>
          {currentRow.name} (#{currentRow.id})
        </>
      }
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            variant='outline'
            onClick={handleResetToGlobal}
            disabled={!isRoot || saveMutation.isPending}
          >
            {t('Reset to Global Defaults')}
          </Button>
          <Button
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={saveMutation.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleSave}
            disabled={!isRoot || saveMutation.isPending || !form}
          >
            {saveMutation.isPending && (
              <Loader2 className='mr-2 size-4 animate-spin' />
            )}
            {t('Save')}
          </Button>
        </>
      }
    >
      {isLoading || !form || !config ? (
        <div className='text-muted-foreground flex items-center gap-2 py-8 text-sm'>
          <Loader2 className='size-4 animate-spin' />
          {t('Loading...')}
        </div>
      ) : (
        <div className='space-y-4 py-2'>
          {isTempDisabled && (
            <Alert>
              <AlertTitle>{t('Temporarily disabled by scheduler')}</AlertTitle>
              <AlertDescription className='flex flex-wrap items-center gap-2'>
                <span>
                  {t('Until')}{' '}
                  <span className='font-mono'>
                    {formatTimestampToDate(config.auto_disabled_until)}
                  </span>
                </span>
                {canManualRestore && (
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={restoreMutation.isPending}
                    onClick={() => restoreMutation.mutate()}
                  >
                    <RotateCcw className='mr-1 size-3.5' />
                    {t('Restore Now')}
                  </Button>
                )}
              </AlertDescription>
            </Alert>
          )}

          <div className='flex items-start justify-between gap-4'>
            <div className='min-w-0 space-y-0.5'>
              <Label className='text-sm'>
                {t('Participate in Advanced Scheduling')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'When off, this channel is never temporarily disabled by the scheduler; it is only skipped within the failing request.'
                )}
              </p>
            </div>
            <Switch
              checked={form.enabled}
              disabled={!isRoot}
              onCheckedChange={(checked) =>
                setForm((prev) => (prev ? { ...prev, enabled: checked } : prev))
              }
            />
          </div>

          <div className='space-y-1'>
            <Label className='text-sm' htmlFor='scheduler-retry-times'>
              {t('Failure Threshold')}
            </Label>
            <Input
              id='scheduler-retry-times'
              type='number'
              min={1}
              disabled={!isRoot}
              placeholder={`${t('Global default')}: ${config.global.channel_failure_threshold}`}
              value={form.retryTimes}
              onChange={(e) =>
                setForm((prev) =>
                  prev ? { ...prev, retryTimes: e.target.value } : prev
                )
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Consecutive failures before this channel is temporarily disabled. Leave empty to use the global default.'
              )}
            </p>
          </div>

          <div className='space-y-1'>
            <Label className='text-sm' htmlFor='scheduler-disable-seconds'>
              {t('Disable Duration (seconds)')}
            </Label>
            <Input
              id='scheduler-disable-seconds'
              type='number'
              min={1}
              disabled={!isRoot}
              placeholder={`${t('Global default')}: ${config.global.auto_disable_seconds}`}
              value={form.disableSeconds}
              onChange={(e) =>
                setForm((prev) =>
                  prev ? { ...prev, disableSeconds: e.target.value } : prev
                )
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'How long this channel stays temporarily disabled. Leave empty to use the global default.'
              )}
            </p>
          </div>

          <div className='flex items-start justify-between gap-4'>
            <div className='min-w-0 space-y-0.5'>
              <Label className='text-sm'>{t('Auto Recover on Expiry')}</Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'When off, the channel stays disabled after expiry and can only be restored manually.'
                )}
              </p>
            </div>
            <Switch
              checked={form.autoRecover}
              disabled={!isRoot}
              onCheckedChange={(checked) =>
                setForm((prev) =>
                  prev ? { ...prev, autoRecover: checked } : prev
                )
              }
            />
          </div>

          <div className='flex items-start justify-between gap-4'>
            <div className='min-w-0 space-y-0.5'>
              <Label className='text-sm'>{t('Allow Manual Restore')}</Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Whether administrators can manually restore this channel while it is temporarily disabled.'
                )}
              </p>
            </div>
            <Switch
              checked={form.manualRestore}
              disabled={!isRoot}
              onCheckedChange={(checked) =>
                setForm((prev) =>
                  prev ? { ...prev, manualRestore: checked } : prev
                )
              }
            />
          </div>

          {!isRoot && (
            <p className='text-muted-foreground text-xs'>
              {t('Only super administrators can modify scheduler settings.')}
            </p>
          )}
        </div>
      )}
    </Dialog>
  )
}

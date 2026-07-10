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

export const LOG_CLEANUP_DEFAULT_RETENTION_HOURS = 24
export const LOG_CLEANUP_HOURS_IN_DAY = 24

export function getLogCleanupTargetTimestamp(date: Date | undefined) {
  if (!date) return null
  return Math.floor(date.getTime() / 1000)
}

export function getLogCleanupDateHoursAgo(hours: number, now = new Date()) {
  const date = new Date(now)
  date.setHours(date.getHours() - hours)
  return date
}

export function getLogCleanupDateDaysAgo(days: number, now = new Date()) {
  return getLogCleanupDateHoursAgo(days * LOG_CLEANUP_HOURS_IN_DAY, now)
}

export function getDefaultLogCleanupDate(now = new Date()) {
  return getLogCleanupDateHoursAgo(LOG_CLEANUP_DEFAULT_RETENTION_HOURS, now)
}

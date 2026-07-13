import assert from 'node:assert/strict'
import { describe, test } from 'node:test'
import {
  getDefaultLogCleanupDate,
  getLogCleanupTargetTimestamp,
} from './log-cleanup-time.ts'

describe('log cleanup target timestamp', () => {
  test('returns null when no cleanup date is selected', () => {
    assert.equal(getLogCleanupTargetTimestamp(undefined), null)
  })

  test('converts the selected cutoff to seconds without dropping time', () => {
    const cutoff = new Date(2026, 6, 9, 18, 41, 45, 999)

    assert.equal(
      getLogCleanupTargetTimestamp(cutoff),
      Math.floor(cutoff.getTime() / 1000)
    )
  })

  test('defaults to 24 hours ago so recent history logs can be cleaned', () => {
    const now = new Date(2026, 6, 10, 22, 22, 0, 0)

    assert.deepEqual(
      getDefaultLogCleanupDate(now),
      new Date(2026, 6, 9, 22, 22, 0, 0)
    )
  })
})

import { describe, expect, it } from 'vitest'
import type { JobTrigger } from './api'
import { formatTime, timeZoneOptions, triggersOf } from './jobs'

describe('formatTime', () => {
  it('writes times of day with two digits', () => {
    expect(formatTime(9, 5)).toBe('09:05')
    expect(formatTime(23, 0)).toBe('23:00')
  })
})

describe('triggersOf', () => {
  it('keeps the triggers of the job, earliest first', () => {
    const triggers: JobTrigger[] = [
      { id: 'b', job: 'hello', hour: 18, minute: 0 },
      { id: 'x', job: 'other', hour: 7, minute: 0 },
      { id: 'a', job: 'hello', hour: 9, minute: 30 },
      { id: 'c', job: 'hello', hour: 9, minute: 5 },
    ]
    expect(triggersOf(triggers, 'hello').map((t) => t.id)).toEqual(['c', 'a', 'b'])
  })
})

describe('timeZoneOptions', () => {
  it('includes UTC and the given zone once, sorted', () => {
    const zones = timeZoneOptions('Asia/Tokyo')
    expect(zones).toContain('UTC')
    expect(zones.filter((z) => z === 'Asia/Tokyo')).toHaveLength(1)
    expect([...zones].sort()).toEqual(zones)
  })

  it('includes every given zone, also one the browser does not list', () => {
    const zones = timeZoneOptions('Asia/Tokyo', 'US/Eastern')
    expect(zones).toContain('Asia/Tokyo')
    expect(zones).toContain('US/Eastern')
  })
})

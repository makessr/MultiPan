export type MeterView = {
  phase: 'waiting' | 'transferring' | 'stalled' | 'checking' | 'complete'
  bytesPerSecond: number | null
  remainingSeconds: number | null
}

type Sample = { time: number; received: number }

// Reads cumulative bytes for one file. The caller supplies a monotonic clock,
// so tests do not need real waits and wall-clock changes cannot distort rates.
export class DownloadMeter {
  private key = ''
  private samples: Sample[] = []
  private received = 0
  private total = -1
  private lastIncrease: number | null = null
  private complete = false

  reset() {
    this.key = ''; this.samples = []; this.received = 0; this.total = -1
    this.lastIncrease = null; this.complete = false
  }

  push(key: string, received: number, total: number, now: number): boolean {
    if (!key || !Number.isSafeInteger(received) || received < 0 ||
        !Number.isSafeInteger(total) || total < -1 || !Number.isFinite(now) || now < 0) return false
    if (key !== this.key || received < this.received) {
      this.reset(); this.key = key; this.samples = [{time: now, received: 0}]
    }
    if (this.complete) return false
    // Zero-byte preparation events can precede slow metadata requests. Start
    // the transfer baseline again when the content stream is actually ready.
    if (received === 0) {
      this.samples = [{time: now, received: 0}]; this.total = total
      return true
    }
    const last = this.samples.at(-1)
    if (last && now < last.time) {
      this.samples = [{time: now, received}]; this.lastIncrease = null
    } else {
      if (received > this.received) this.lastIncrease = now
      if (last?.time === now) this.samples[this.samples.length - 1] = {time: now, received}
      else this.samples.push({time: now, received})
    }
    this.received = received; this.total = total
    while (this.samples.length > 2 && this.samples[1]!.time < now - 5000) this.samples.shift()
    while (this.samples.length > 128) this.samples.shift()
    return true
  }

  finish(key: string) {
    if (key === this.key) this.complete = true
  }

  read(now: number): MeterView {
    const empty = (phase: MeterView['phase']): MeterView => ({phase, bytesPerSecond: null, remainingSeconds: null})
    if (this.complete) return empty('complete')
    if (!this.key || !Number.isFinite(now) || now < 0) return empty('waiting')
    if (this.total >= 0 && this.received >= this.total) return empty('checking')
    if (this.lastIncrease === null) return empty('waiting')
    if (now - this.lastIncrease >= 3000) return {phase: 'stalled', bytesPerSecond: 0, remainingSeconds: null}
    const first = this.samples[0]
    if (!first || now - first.time < 500) return empty('transferring')
    const rate = (this.received - first.received) * 1000 / (now - first.time)
    if (!Number.isFinite(rate) || rate <= 0) return empty('transferring')
    const remaining = this.total >= this.received ? (this.total - this.received) / rate : null
    return {phase: 'transferring', bytesPerSecond: rate, remainingSeconds: remaining !== null && Number.isFinite(remaining) ? remaining : null}
  }
}

export function remainingTime(seconds: number | null): string {
  if (seconds === null || !Number.isFinite(seconds) || seconds < 0) return '暂无法估算'
  if (seconds < 1) return '不到1秒'
  const value = Math.ceil(seconds)
  if (value < 60) return value + '秒'
  if (value < 3600) return Math.floor(value / 60) + '分' + value % 60 + '秒'
  if (value < 86400) return Math.floor(value / 3600) + '小时' + Math.floor(value % 3600 / 60) + '分'
  return Math.floor(value / 86400) + '天' + Math.floor(value % 86400 / 3600) + '小时'
}

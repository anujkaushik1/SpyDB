// 0 → "never", 40 s → "just now", 5 min → "5 minutes ago", 3 h → "3 hours ago", …
// now is a parameter so tests can fix the clock.
export function timeAgo(ms: number, now = Date.now()) {
  if (!ms) return 'never'
  const minutes = Math.floor((now - ms) / 60000)
  if (minutes < 1) return 'just now'
  const unit = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'} ago`
  if (minutes < 60) return unit(minutes, 'minute')
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return unit(hours, 'hour')
  const days = Math.floor(hours / 24)
  if (days < 30) return unit(days, 'day')
  const months = Math.floor(days / 30)
  return months < 12 ? unit(months, 'month') : unit(Math.floor(months / 12), 'year')
}

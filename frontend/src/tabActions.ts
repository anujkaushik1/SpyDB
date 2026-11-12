// What the tab menu's Close commands close, and which tab is active after.

export type CloseCommand = 'close' | 'others' | 'right' | 'all'

// The tabs (ids, in tab-bar order) that `command` on tab `id` closes.
export function tabsToClose(ids: string[], id: string, command: CloseCommand): string[] {
  const at = ids.indexOf(id)
  if (at < 0) return []
  switch (command) {
    case 'close': return [id]
    case 'others': return ids.filter((x) => x !== id)
    case 'right': return ids.slice(at + 1)
    case 'all': return [...ids]
  }
}

// The active tab once `closing` are closed: the same one if it stays open;
// else `from` (the tab the command was used on) if it stays; else the
// nearest open tab to the left of where the active one was, or the first
// one left; '' when none is.
export function activeAfterClose(ids: string[], closing: string[], active: string, from?: string): string {
  const gone = new Set(closing)
  if (!gone.has(active)) return active
  if (from !== undefined && ids.includes(from) && !gone.has(from)) return from
  for (let i = ids.indexOf(active) - 1; i >= 0; i--) {
    if (!gone.has(ids[i])) return ids[i]
  }
  return ids.find((x) => !gone.has(x)) ?? ''
}

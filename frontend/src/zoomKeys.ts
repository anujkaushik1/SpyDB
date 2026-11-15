// Ctrl + / Ctrl - / Ctrl 0 zoom the window on Linux and Windows, as ⌘+ / ⌘- / ⌘0
// do on macOS. macOS gets those from Wails' menu bar (View menu). Linux and
// Windows have no menu bar, and Wails' Linux key table has no + - = or 0, so a
// Go key binding would never fire: the page catches the keys instead (main.tsx).

export type ZoomAction = 'in' | 'out' | 'reset'

type Keys = { key: string; code: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean }

// Which zoom a key press asks for, or null. Ctrl with the +/= key (Shift or
// not) or the keypad's +; - (or _) or the keypad's -; 0 or the keypad's 0.
export function zoomAction(e: Keys): ZoomAction | null {
  if (!e.ctrlKey || e.metaKey || e.altKey) return null
  if (e.key === '+' || e.key === '=' || e.code === 'NumpadAdd') return 'in'
  if (e.key === '-' || e.key === '_' || e.code === 'NumpadSubtract') return 'out'
  if (e.key === '0' || e.code === 'Numpad0') return 'reset'
  return null
}

// Which OS the app runs on, for labels: ⌘ and Finder on macOS, Ctrl and
// "folder" on Linux (and Windows). The shortcuts themselves accept both ⌘ and
// Ctrl everywhere. No React or Wails imports, so the unit tests can load it.

// WebKit reports "MacIntel" on macOS and e.g. "Linux x86_64" on Ubuntu.
export function detectMac(platform: string, userAgent = '') {
  return /mac/i.test(platform) || (!platform && /Macintosh|Mac OS X/i.test(userAgent))
}

export const isMac = typeof navigator !== 'undefined' && detectMac(navigator.platform ?? '', navigator.userAgent ?? '')

// The modifier key's label: "⌘" or "Ctrl".
export const modKey = (mac = isMac) => (mac ? '⌘' : 'Ctrl')

// A shortcut's label: "⌘S" on macOS, "Ctrl+S" elsewhere.
export const shortcut = (key: string, mac = isMac) => (mac ? `⌘${key}` : `Ctrl+${key}`)

// Where "Show in …" opens a file.
export const fileManager = (mac = isMac) => (mac ? 'Finder' : 'folder')

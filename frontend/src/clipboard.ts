import { Clipboard } from '@wailsio/runtime'

// Copies to the system clipboard through Wails, or the browser API where
// Wails can't (the server-mode build).
export async function copyText(text: string) {
  try {
    await Clipboard.SetText(text)
  } catch {
    await navigator.clipboard.writeText(text)
  }
}

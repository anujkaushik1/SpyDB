import React from 'react'
import ReactDOM from 'react-dom/client'
import { ZoomService } from '../bindings/github.com/anujkaushik1/SpyDB'
import App from './App'
import { isMac } from './platform'
import { zoomAction } from './zoomKeys'

// macOS draws its own header under the traffic lights (see .titlebar);
// Linux and Windows show the system title bar instead.
document.documentElement.dataset.platform = isMac ? 'mac' : 'other'

// Ctrl + / Ctrl - / Ctrl 0 zoom the window on Linux and Windows (see
// zoomKeys.ts); Go (zoom.go) zooms it and saves the zoom, so SpyDB opens at
// it next time. Not on macOS, where ⌘+ / ⌘- / ⌘0 already come from the menu
// bar. Caught before anything else on the page (capture), so an editor or the
// grid can't keep the key.
if (!isMac) {
  window.addEventListener('keydown', (e) => {
    const action = zoomAction(e)
    if (!action) return
    e.preventDefault()
    const zoom = action === 'in' ? ZoomService.ZoomIn() : action === 'out' ? ZoomService.ZoomOut() : ZoomService.ZoomReset()
    zoom.catch(() => {}) // zoomed even if saving it failed; nothing to zoom in a browser
  }, true)
}

// A Go call cancelled on purpose (Stop, a closed tab) may still end in an
// error afterwards; the Wails runtime reports that as an unhandled rejection.
// It's expected: nothing is waiting for that answer any more.
window.addEventListener('unhandledrejection', (e) => {
  if ((e.reason as Error | null)?.name === 'CancelledRejectionError') e.preventDefault()
})

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)

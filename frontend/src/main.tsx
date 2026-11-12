import React from 'react'
import ReactDOM from 'react-dom/client'
import { Window } from '@wailsio/runtime'
import App from './App'
import { isMac } from './platform'
import { zoomAction } from './zoomKeys'

// macOS draws its own header under the traffic lights (see .titlebar);
// Linux and Windows show the system title bar instead.
document.documentElement.dataset.platform = isMac ? 'mac' : 'other'

// Ctrl + / Ctrl - / Ctrl 0 zoom the window on Linux and Windows (see
// zoomKeys.ts). Not on macOS, where ⌘+ / ⌘- / ⌘0 already come from the menu
// bar. Caught before anything else on the page (capture), so an editor or the
// grid can't keep the key.
if (!isMac) {
  window.addEventListener('keydown', (e) => {
    const action = zoomAction(e)
    if (!action) return
    e.preventDefault()
    const zoom = action === 'in' ? Window.ZoomIn() : action === 'out' ? Window.ZoomOut() : Window.ZoomReset()
    zoom.catch(() => {}) // nothing to zoom outside the app window (a browser)
  }, true)
}

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)

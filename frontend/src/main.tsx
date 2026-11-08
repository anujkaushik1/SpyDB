import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { isMac } from './platform'

// macOS draws its own header under the traffic lights (see .titlebar);
// Linux and Windows show the system title bar instead.
document.documentElement.dataset.platform = isMac ? 'mac' : 'other'

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)

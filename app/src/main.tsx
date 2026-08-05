import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

// Fonts are bundled, not fetched from a CDN. Two reasons: the CSP is
// `default-src 'self'`, and a self-hosted instance should not phone Google on
// every page load — including one running with no internet at all.
import '@fontsource-variable/fraunces'
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'

import './base.css'
import './views.css'
import { App } from './App'
import { SessionProvider } from './state/session'

const root = document.getElementById('root')
if (!root) throw new Error('#root is missing from index.html')

createRoot(root).render(
  <StrictMode>
    <SessionProvider>
      <App />
    </SessionProvider>
  </StrictMode>,
)

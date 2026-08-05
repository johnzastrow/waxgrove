import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'

/**
 * Drops the legacy .woff copies of every font, and the `url(...) format('woff')`
 * fallbacks that point at them.
 *
 * @fontsource ships .woff alongside .woff2 for browsers that predate woff2 —
 * none of which can run a PWA built from ES modules anyway. Keeping them would
 * roughly double the font payload in both the repo and the image, for nobody.
 */
function woff2Only(): Plugin {
  return {
    name: 'waxgrove:woff2-only',
    generateBundle(_options, bundle) {
      for (const [file, asset] of Object.entries(bundle)) {
        if (file.endsWith('.woff')) {
          delete bundle[file]
          continue
        }
        // Strip the now-dangling fallback so no browser requests a 404.
        if (file.endsWith('.css') && asset.type === 'asset') {
          const css = String(asset.source)
          asset.source = css.replace(/,\s*url\([^)]*\.woff\)\s*format\(["']woff["']\)/g, '')
        }
      }
    },
  }
}

// The build output lands inside the Go module so internal/webui can go:embed
// it. That keeps `go build` self-contained: a checkout with no Node installed
// still produces a complete binary (see internal/webui/embed.go).
export default defineConfig({
  plugins: [
    react(),
    woff2Only(),
    VitePWA({
      registerType: 'prompt', // never swap the running app out from under a user
      injectRegister: null,   // registration is explicit in src/pwa.ts
      manifest: {
        name: 'Waxgrove',
        short_name: 'Waxgrove',
        description: 'A shared record crate for friends on different streaming services.',
        start_url: '/',
        scope: '/',
        display: 'standalone',
        orientation: 'portrait-primary',
        background_color: '#0B0C09',
        theme_color: '#0B0C09',
        icons: [
          { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
          { src: '/icons/mark.svg', sizes: 'any', type: 'image/svg+xml' },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,woff2}'],
        // The API is never precached and never served stale: a playlist read
        // from a week-old cache would silently contradict the server.
        navigateFallbackDenylist: [/^\/api\//, /^\/health$/],
        runtimeCaching: [],
      },
    }),
  ],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    // No data: URIs for small assets — the CSP is `default-src 'self'` and
    // inlining would force it open for no real benefit.
    assetsInlineLimit: 0,
    sourcemap: false,
  },
  server: {
    port: 5173,
    // `npm run dev` proxies to a locally running `waxgrove` binary so the
    // session cookie behaves exactly as it does in production.
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/health': 'http://127.0.0.1:8080',
    },
  },
})

import { defineConfig } from 'vite'

// base: './' keeps every built asset reference relative, so the dist output
// can be served from any sub-path — e.g. a sub2api plugin runtime
// (/plugin-runtime/:id/ui/) or a static file server — without touching the
// hosting site's root.
export default defineConfig({
  base: './'
})

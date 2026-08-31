# WatchWeaver web frontend

This directory contains WatchWeaver's React + TypeScript frontend, built with Vite.

From this directory:

```bash
npm ci
npm run test
npm run typecheck
npm run build
npm run dev
```

Production assets are written to `dist/`. When the repository-root `web/dist/index.html` exists, the WatchWeaver Go process serves those assets on the same origin as the backend endpoints.

This scaffold intentionally contains only the minimum application shell. Dashboard structure, integration setup flows, and product-level UI decisions are implemented in later scoped issues.

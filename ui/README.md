# SwitchFrame UI

Svelte 5 + SvelteKit frontend for SwitchFrame. See the [root README](../README.md) for full project documentation.

## Development

```bash
npm ci              # Install dependencies
npm run dev         # Dev server (proxies /api to Go server on :8081)
npx vitest run      # Unit tests (590 tests)
npx playwright test # E2E tests (47 tests)
```

## Tech Stack

- **Svelte 5** with runes syntax (`$state`, `$derived`, `$effect`)
- **SvelteKit** with `adapter-static` (SPA mode, embedded in Go binary for production)
- **WebTransport/MoQ** for low-latency media and state sync
- **WebCodecs** for H.264/AAC decode (video in Web Worker, audio via AudioWorklet)
- **SharedArrayBuffer** for lock-free audio ring buffer

## Build

```bash
npm run build       # Static build → build/
```

The production build is embedded into the Go binary via `//go:embed` with the `embed_ui` build tag.

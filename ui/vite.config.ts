import { sveltekit } from '@sveltejs/kit/vite';
import { svelteTesting } from '@testing-library/svelte/vite';
import { defineConfig, type Plugin } from 'vitest/config';

/** Vite plugin that enables cross-origin isolation for SharedArrayBuffer. */
function crossOriginIsolation(): Plugin {
	return {
		name: 'cross-origin-isolation',
		configureServer(server) {
			server.middlewares.use((_req, res, next) => {
				res.setHeader('Cross-Origin-Opener-Policy', 'same-origin');
				res.setHeader('Cross-Origin-Embedder-Policy', 'credentialless');
				next();
			});
		},
	};
}

export default defineConfig({
	plugins: [crossOriginIsolation(), sveltekit(), svelteTesting()],
	build: {
		rollupOptions: {
			output: {
				// Multi-region resilience: bundle everything into one chunk so lazy-loaded
			// modules don't fail when the serving engine dies. This is a pure SPA
			// (ssr=false, prerender=false) so single-output mode is safe. Adding
			// SSR or additional routes would require removing this setting.
			inlineDynamicImports: true,
			},
		},
	},
	test: {
		include: ['src/**/*.test.ts'],
		environment: 'jsdom',
		setupFiles: ['src/test-setup.ts'],
	},
	server: {
		proxy: {
			'/api': {
				target: 'http://localhost:8081',
			},
		},
	},
});

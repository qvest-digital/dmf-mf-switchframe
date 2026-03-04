import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [sveltekit()],
	test: {
		include: ['src/**/*.test.ts'],
		environment: 'jsdom',
	},
	server: {
		proxy: {
			'/api': {
				target: 'https://localhost:8080',
				secure: false,
			},
		},
	},
});

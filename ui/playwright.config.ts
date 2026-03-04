import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: './e2e',
	webServer: {
		command: 'npm run build && npx serve build -l 4173',
		port: 4173,
	},
	use: {
		baseURL: 'http://localhost:4173',
	},
});

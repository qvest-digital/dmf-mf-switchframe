import { test, expect, type Route } from '@playwright/test';

test.describe('operator sessions on both engines', () => {
	// A page load reconnects the operator before the initial state names
	// engine B, so the reconnect reaches engine A only. Engine B, holding no
	// session for the operator, would refuse every lock the operator takes.
	test('an operator reconnects at engine B once it is discovered', async ({ page }) => {
		const engineB = 'http://engine-b.test';
		const token = 'ab'.repeat(32);
		const op = { id: 'op-0123456789', name: 'alice', role: 'director' };
		const reconnects: string[] = [];

		await page.route('/auth/config', (route) => route.fulfill({ json: { enabled: false } }));
		const engine = (origin: string) => (route: Route) => {
			const url = new URL(route.request().url());
			if (url.pathname === '/api/operator/reconnect') reconnects.push(origin);
			if (url.pathname === '/api/switch/state') {
				return route.fulfill({ json: { programSource: '', previewSource: '', sources: {}, engineBUrl: engineB, seq: 1 } });
			}
			if (url.pathname.startsWith('/api/operator/')) return route.fulfill({ json: op });
			return route.fulfill({ status: 404, json: { error: 'not mocked' } });
		};
		await page.route('/api/**', engine('A'));
		await page.route(`${engineB}/**`, engine('B'));
		await page.addInitScript((t) => localStorage.setItem('switchframe_operator_token', t), token);

		await page.goto('/');
		await expect.poll(() => reconnects.includes('B'), { timeout: 10_000 }).toBe(true);
	});
});

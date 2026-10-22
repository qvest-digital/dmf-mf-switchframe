import { test, expect, type Page } from '@playwright/test';

// The engine is mocked: /api/** answers 401 until the request carries the
// expected bearer token, and /auth/config says whether sign-in is offered.

/** An unsigned JWT; the UI only reads exp, the engines verify. */
function jwt(expSeconds: number): string {
	const b64 = (o: unknown) => Buffer.from(JSON.stringify(o)).toString('base64url');
	return `${b64({ alg: 'RS256' })}.${b64({ exp: expSeconds, preferred_username: 'alice' })}.sig`;
}

async function mockEngine(page: Page, opts: { oidc: boolean; acceptToken?: string }) {
	const seen: string[] = [];
	await page.route('/auth/config', (route) => route.fulfill({
		contentType: 'application/json',
		body: JSON.stringify(opts.oidc ? { enabled: true } : { enabled: false }),
	}));
	await page.route('/api/**', (route) => {
		const auth = route.request().headers()['authorization'] ?? '';
		seen.push(`${new URL(route.request().url()).pathname} ${auth}`);
		const ok = opts.acceptToken !== undefined && auth === `Bearer ${opts.acceptToken}`;
		return route.fulfill({
			status: ok ? 503 : 401,
			contentType: 'application/json',
			body: JSON.stringify({ error: ok ? 'mock engine' : 'unauthorized' }),
		});
	});
	return seen;
}

test.describe('OpenID Connect sign-in', () => {
	test('an engine without sign-in asks for an API token only', async ({ page }) => {
		await mockEngine(page, { oidc: false });
		await page.goto('/');
		await expect(page.locator('.token-prompt')).toBeVisible();
		await expect(page.locator('.token-box')).toContainText('API token required');
		await expect(page.locator('.token-box .sign-in')).toHaveCount(0);
	});

	test('an engine with sign-in offers it, and Sign in starts the engine flow', async ({ page }) => {
		await mockEngine(page, { oidc: true });
		await page.route('/auth/login', (route) => route.fulfill({ body: 'provider' }));
		await page.goto('/');
		const signIn = page.locator('.token-box .sign-in');
		await expect(signIn).toBeVisible();
		// The API token field stays for scripts and the session owner.
		await expect(page.locator('.token-box input[type="password"]')).toBeVisible();

		await Promise.all([page.waitForURL('**/auth/login'), signIn.click()]);
	});

	test('the ID token from the callback becomes the bearer token and leaves the address bar', async ({ page }) => {
		const idToken = jwt(Math.floor(Date.now() / 1000) + 3600);
		const seen = await mockEngine(page, { oidc: true, acceptToken: idToken });
		await page.goto(`/#id_token=${idToken}&refresh_token=rt-1`);

		await expect.poll(() => seen.includes(`/api/switch/state Bearer ${idToken}`)).toBe(true);
		expect(new URL(page.url()).hash).toBe('');
		expect(await page.evaluate(() => localStorage.getItem('switchframe_oidc_refresh'))).toBe('rt-1');
		await expect(page.locator('.token-prompt')).toHaveCount(0);
	});

	test('a token that lapsed while the page was closed is renewed before the first request', async ({ page }) => {
		const lapsed = jwt(Math.floor(Date.now() / 1000) - 3600);
		const fresh = jwt(Math.floor(Date.now() / 1000) + 3600);
		const seen = await mockEngine(page, { oidc: true, acceptToken: fresh });
		await page.route('/auth/refresh', (route) => route.fulfill({
			contentType: 'application/json',
			body: JSON.stringify({ id_token: fresh, refresh_token: 'rt-2' }),
		}));
		await page.addInitScript(([token]) => {
			if (!sessionStorage.getItem('seeded')) {
				localStorage.setItem('switchframe_operator_token', token);
				localStorage.setItem('switchframe_oidc_refresh', 'rt-1');
				sessionStorage.setItem('seeded', '1');
			}
		}, [lapsed]);
		await page.goto('/');

		// The state request decides whether the page opens on the prompt.
		await expect.poll(() => seen.includes(`/api/switch/state Bearer ${fresh}`)).toBe(true);
		expect(seen).not.toContain(`/api/switch/state Bearer ${lapsed}`);
		await expect(page.locator('.token-prompt')).toHaveCount(0);
	});
});

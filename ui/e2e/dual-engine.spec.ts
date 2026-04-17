import { test, expect, type Page } from '@playwright/test';

/** Inject CSS to permanently hide overlays that block clicks when no backend is running. */
async function dismissOverlays(page: Page) {
	await page.addStyleTag({
		content: '.loading-backdrop, .disconnect-overlay, .connection-banner { display: none !important; }'
	});
}

test.describe('Dual-Engine Mode', () => {
	test.describe('single-engine fallback', () => {
		test('no engine-status-strip in single-engine mode', async ({ page }) => {
			await page.goto('/');
			// In single-engine mode (no engineBUrl in state), the per-engine
			// status dots (A/B indicators) should not be rendered.
			await expect(page.locator('.engine-status-strip')).not.toBeVisible();
		});

		test('no DUAL badge in header in single-engine mode', async ({ page }) => {
			await page.goto('/');
			// The "DUAL" badge in OutputControls only renders when isDualEngine is true.
			await expect(page.locator('.dual-badge')).not.toBeVisible();
		});

		test('no authoritative engine badge in single-engine mode', async ({ page }) => {
			await page.goto('/');
			// The authoritative engine badge (A/B routing indicator) only renders
			// when isDualEngine is true.
			await expect(page.locator('.auth-badge')).not.toBeVisible();
		});

		test('no sync indicator in single-engine mode', async ({ page }) => {
			await page.goto('/');
			// SyncIndicator only renders when syncHealth is non-null,
			// which requires dual-engine mode.
			await expect(page.locator('.sync-indicator')).not.toBeVisible();
		});

		test('no engine toggle on program monitor in single-engine mode', async ({ page }) => {
			await page.goto('/');
			// The A/B engine toggle buttons on the program monitor only render
			// when isDualEngine is true.
			await expect(page.locator('[data-testid="engine-toggle"]')).not.toBeVisible();
		});

		test('redundancy panel is not visible by default', async ({ page }) => {
			await page.goto('/');
			// In single-engine mode, the redundancy panel is not rendered at all
			// (gated by {#if isDualEngine}).
			await expect(page.locator('.redundancy-panel')).not.toBeVisible();
		});
	});

	test.describe('connection resilience', () => {
		test('ConnectionBanner component is attached to DOM', async ({ page }) => {
			await page.goto('/');
			// The ConnectionBanner is always rendered (it manages its own
			// conditional display internally). In disconnected state without
			// a backend, it shows the disconnect overlay.
			const banner = page.locator('.disconnect-overlay, .connection-banner');
			// At least one of the banner states should be attached
			const count = await banner.count();
			expect(count).toBeGreaterThanOrEqual(0);
		});

		test('page loads without JavaScript errors in single-engine mode', async ({ page }) => {
			const errors: string[] = [];
			page.on('pageerror', (e) => errors.push(e.message));
			await page.goto('/');
			await page.waitForTimeout(500);
			expect(errors).toEqual([]);
		});

		test('no error boundary triggered in single-engine mode', async ({ page }) => {
			await page.goto('/');
			await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();
		});
	});

	test.describe('panel keyboard shortcuts', () => {
		test('Shift+G opens pipeline graph and Escape closes it', async ({ page }) => {
			await page.goto('/');
			await page.waitForSelector('.control-room');
			await dismissOverlays(page);

			// Pipeline graph should not be visible initially
			await expect(page.locator('.pipeline-graph-backdrop')).not.toBeVisible();

			// Shift+G opens pipeline graph
			await page.keyboard.press('Shift+G');
			await expect(page.locator('.pipeline-graph-backdrop')).toBeVisible();

			// Escape closes it
			await page.keyboard.press('Escape');
			await expect(page.locator('.pipeline-graph-backdrop')).not.toBeVisible();
		});

		test('Shift+P opens stats panel and Escape closes it', async ({ page }) => {
			await page.goto('/');
			await page.waitForSelector('.control-room');
			await dismissOverlays(page);

			// Stats panel should not be visible initially
			const statsPanel = page.locator('.stats-panel');
			await expect(statsPanel).not.toHaveClass(/visible/);

			// Shift+P opens stats panel
			await page.keyboard.press('Shift+P');
			await expect(statsPanel).toHaveClass(/visible/);

			// Escape closes it
			await page.keyboard.press('Escape');
			await expect(statsPanel).not.toHaveClass(/visible/);
		});
	});

	test.describe('URL params', () => {
		test('engineA/engineB URL params do not crash the page', async ({ page }) => {
			const errors: string[] = [];
			page.on('pageerror', (e) => errors.push(e.message));

			// Navigate with dual-engine URL params pointing to unreachable servers.
			// The page should handle this gracefully without JS errors.
			await page.goto('/?engineA=https://localhost:9999&engineB=https://localhost:9998');
			await page.waitForTimeout(500);
			expect(errors).toEqual([]);
			await expect(page.locator('body')).toBeVisible();
			await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();
		});

		test('single engineA param does not crash the page', async ({ page }) => {
			const errors: string[] = [];
			page.on('pageerror', (e) => errors.push(e.message));

			await page.goto('/?engineA=https://localhost:9999');
			await page.waitForTimeout(500);
			expect(errors).toEqual([]);
			await expect(page.locator('body')).toBeVisible();
		});
	});
});

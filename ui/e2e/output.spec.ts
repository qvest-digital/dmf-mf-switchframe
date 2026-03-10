import { test, expect, type Page } from '@playwright/test';

/** Inject CSS to permanently hide overlays that block clicks when no backend is running. */
async function dismissOverlays(page: Page) {
	await page.addStyleTag({
		content: '.loading-backdrop, .disconnect-overlay, .connection-banner { display: none !important; }'
	});
}

test.describe('Output Controls', () => {
	test('renders REC button in header', async ({ page }) => {
		await page.goto('/');
		const recBtn = page.locator('.rec-start');
		await expect(recBtn).toBeVisible();
		await expect(recBtn).toHaveText('REC');
	});

	test('renders SRT button in header', async ({ page }) => {
		await page.goto('/');
		const srtBtn = page.locator('.srt-btn');
		await expect(srtBtn).toBeVisible();
		await expect(srtBtn).toHaveText('SRT');
	});

	test('SRT button opens modal', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		await expect(page.locator('.srt-modal')).toBeVisible();
		// Default mode is "caller", so both radio options should be visible
		await expect(page.locator('.mode-option').filter({ hasText: 'Caller' })).toBeVisible();
		await expect(page.locator('.mode-option').filter({ hasText: 'Listener' })).toBeVisible();
	});

	test('SRT modal has port field', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		const portInput = page.locator('input[name="port"]');
		await expect(portInput).toBeVisible();
		// Default port is 9000
		await expect(portInput).toHaveValue('9000');
	});

	test('SRT modal shows address field in caller mode', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		// Default mode is "caller" so address field should be visible
		const addressInput = page.locator('input[name="address"]');
		await expect(addressInput).toBeVisible();
	});

	test('SRT modal shows latency field', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		const latencyInput = page.locator('input[name="latency"]');
		await expect(latencyInput).toBeVisible();
		await expect(latencyInput).toHaveValue('200');
	});

	test('SRT modal hides address field in listener mode', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		// Switch to listener mode
		await page.locator('.mode-option').filter({ hasText: 'Listener' }).click();
		// Address field should be hidden in listener mode
		const addressInput = page.locator('input[name="address"]');
		await expect(addressInput).not.toBeVisible();
		// Port should still be visible
		await expect(page.locator('input[name="port"]')).toBeVisible();
	});

	test('SRT modal can be closed via close button', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		await expect(page.locator('.srt-modal')).toBeVisible();
		await page.locator('.srt-modal .close-btn').click();
		await expect(page.locator('.srt-modal')).not.toBeVisible();
	});

	test('SRT modal can be closed via backdrop click', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		await expect(page.locator('.srt-modal')).toBeVisible();
		// Click the backdrop (top-left corner, outside the modal)
		await page.locator('.srt-modal-backdrop').click({ position: { x: 5, y: 5 } });
		await expect(page.locator('.srt-modal')).not.toBeVisible();
	});

	test('SRT modal has Start button', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.srt-btn').click();
		const startBtn = page.locator('.start-btn');
		await expect(startBtn).toBeVisible();
		await expect(startBtn).toHaveText('Start');
	});

	test('page loads without console errors', async ({ page }) => {
		const errors: string[] = [];
		page.on('pageerror', (e) => errors.push(e.message));
		await page.goto('/');
		await page.waitForTimeout(500);
		expect(errors).toEqual([]);
	});
});

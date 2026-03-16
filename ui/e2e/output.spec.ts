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

	test('renders I/O button in header', async ({ page }) => {
		await page.goto('/');
		const ioBtn = page.locator('.io-btn');
		await expect(ioBtn).toBeVisible();
		await expect(ioBtn).toHaveText('I/O');
	});

	test('I/O button opens panel', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.io-btn').click();
		const panel = page.locator('.io-panel');
		await expect(panel).toHaveClass(/visible/);
		await expect(panel.locator('text=INPUTS')).toBeVisible();
		await expect(panel.locator('text=OUTPUTS')).toBeVisible();
	});

	test('I/O panel has Add SRT Source button', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.io-btn').click();
		await expect(page.locator('text=Add SRT Source')).toBeVisible();
	});

	test('I/O panel has Add Destination button', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.io-btn').click();
		await expect(page.locator('text=Add Destination')).toBeVisible();
	});

	test('I/O panel shows recording inactive when not recording', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.io-btn').click();
		await expect(page.locator('text=Recording inactive')).toBeVisible();
	});

	test('I/O panel can be closed via close button', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.io-btn').click();
		const panel = page.locator('.io-panel');
		await expect(panel).toHaveClass(/visible/);
		await page.locator('.io-panel .close-btn').click();
		await expect(panel).not.toHaveClass(/visible/);
	});

	test('I/O panel can be closed via Escape', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		await page.locator('.io-btn').click();
		const panel = page.locator('.io-panel');
		await expect(panel).toHaveClass(/visible/);
		await page.keyboard.press('Escape');
		await expect(panel).not.toHaveClass(/visible/);
	});

	test('I/O panel toggles on repeated I/O button clicks', async ({ page }) => {
		await page.goto('/');
		await dismissOverlays(page);
		const panel = page.locator('.io-panel');
		await page.locator('.io-btn').click();
		await expect(panel).toHaveClass(/visible/);
		await page.locator('.io-btn').click();
		await expect(panel).not.toHaveClass(/visible/);
	});

	test('CONFIRM button is visible', async ({ page }) => {
		await page.goto('/');
		const confirmBtn = page.locator('.confirm-btn');
		await expect(confirmBtn).toBeVisible();
		await expect(confirmBtn).toHaveText('CONFIRM');
	});

	test('page loads without console errors', async ({ page }) => {
		const errors: string[] = [];
		page.on('pageerror', (e) => errors.push(e.message));
		await page.goto('/');
		await page.waitForTimeout(500);
		expect(errors).toEqual([]);
	});
});

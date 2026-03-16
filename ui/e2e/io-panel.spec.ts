import { test, expect, type Page } from '@playwright/test';

/** Inject CSS to permanently hide overlays that block clicks when no backend is running. */
async function dismissOverlays(page: Page) {
	await page.addStyleTag({
		content: '.loading-backdrop, .disconnect-overlay, .connection-banner { display: none !important; }'
	});
}

test.describe('I/O Panel', () => {
	test('Shift+I opens the I/O panel', async ({ page }) => {
		await page.goto('/');
		await page.waitForSelector('.control-room');
		const panel = page.locator('.io-panel');
		await expect(panel).not.toHaveClass(/visible/);
		await page.keyboard.press('Shift+I');
		await expect(panel).toHaveClass(/visible/);
		await expect(panel.locator('text=INPUTS')).toBeVisible();
		await expect(panel.locator('text=OUTPUTS')).toBeVisible();
	});

	test('I/O button in header opens panel', async ({ page }) => {
		await page.goto('/');
		await page.waitForSelector('.control-room');
		await dismissOverlays(page);
		await page.click('.io-btn');
		const panel = page.locator('.io-panel');
		await expect(panel).toHaveClass(/visible/);
	});

	test('Escape closes I/O panel', async ({ page }) => {
		await page.goto('/');
		await page.waitForSelector('.control-room');
		await page.keyboard.press('Shift+I');
		const panel = page.locator('.io-panel');
		await expect(panel).toHaveClass(/visible/);
		await page.keyboard.press('Escape');
		await expect(panel).not.toHaveClass(/visible/);
	});

	test('panel shows section headers and add-source button', async ({ page }) => {
		await page.goto('/');
		await page.waitForSelector('.control-room');
		await page.keyboard.press('Shift+I');
		const panel = page.locator('.io-panel');
		// Section headers are always present
		await expect(panel.locator('.section-label').first()).toBeVisible();
		// "Add SRT Source" and "Add Destination" buttons are always visible
		await expect(panel.locator('.add-btn').first()).toBeVisible();
	});
});

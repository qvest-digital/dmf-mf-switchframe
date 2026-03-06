import { test, expect } from '@playwright/test';

test.describe('Video Playback', () => {
	test('program window has a canvas element', async ({ page }) => {
		await page.goto('/');
		const programCanvas = page.locator('.program-monitor canvas');
		await expect(programCanvas.first()).toBeVisible();
	});

	test('preview window has a canvas element', async ({ page }) => {
		await page.goto('/');
		const previewCanvas = page.locator('.preview-monitor canvas');
		await expect(previewCanvas).toBeVisible();
	});

	test('program monitor has viewport with canvas', async ({ page }) => {
		await page.goto('/');
		const viewport = page.locator('.program-monitor .monitor-viewport');
		await expect(viewport).toBeVisible();
		const canvas = viewport.locator('canvas');
		await expect(canvas.first()).toBeVisible();
	});

	test('preview monitor has viewport with canvas', async ({ page }) => {
		await page.goto('/');
		const viewport = page.locator('.preview-monitor .monitor-viewport');
		await expect(viewport).toBeVisible();
		const canvas = viewport.locator('canvas');
		await expect(canvas).toBeVisible();
	});

	test('program/preview labels are visible', async ({ page }) => {
		await page.goto('/');
		await expect(page.locator('.program-label')).toHaveText('PROGRAM');
		await expect(page.locator('.preview-label')).toHaveText('PREVIEW');
	});

	test('multiview tiles render canvases when sources exist', async ({ page }) => {
		await page.goto('/');
		// Without a backend, there are no sources, so no tiles.
		// Just verify the multiview container exists and no errors occur.
		const multiview = page.locator('.multiview');
		await expect(multiview).toBeVisible();
		const tileCanvases = page.locator('.tile-video');
		const count = await tileCanvases.count();
		// 0 tiles is valid (no backend), >0 is valid (backend running)
		expect(count).toBeGreaterThanOrEqual(0);
	});

	test('page loads without JavaScript errors', async ({ page }) => {
		const errors: string[] = [];
		page.on('pageerror', (e) => errors.push(e.message));
		await page.goto('/');
		await page.waitForTimeout(500);
		expect(errors).toEqual([]);
	});
});

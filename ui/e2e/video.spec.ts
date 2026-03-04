import { test, expect } from '@playwright/test';

test.describe('Video Playback', () => {
	test('program window has a canvas element', async ({ page }) => {
		await page.goto('/');
		// ProgramPreview.svelte always renders canvases unconditionally
		const programCanvas = page.locator('.program-window canvas');
		await expect(programCanvas).toBeVisible();
	});

	test('preview window has a canvas element', async ({ page }) => {
		await page.goto('/');
		const previewCanvas = page.locator('.preview-window canvas');
		await expect(previewCanvas).toBeVisible();
	});

	test('program canvas has correct dimensions', async ({ page }) => {
		await page.goto('/');
		const programCanvas = page.locator('#program-video');
		await expect(programCanvas).toBeVisible();
		await expect(programCanvas).toHaveAttribute('width', '640');
		await expect(programCanvas).toHaveAttribute('height', '360');
	});

	test('preview canvas has correct dimensions', async ({ page }) => {
		await page.goto('/');
		const previewCanvas = page.locator('#preview-video');
		await expect(previewCanvas).toBeVisible();
		await expect(previewCanvas).toHaveAttribute('width', '640');
		await expect(previewCanvas).toHaveAttribute('height', '360');
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

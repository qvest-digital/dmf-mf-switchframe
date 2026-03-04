import { test, expect } from '@playwright/test';

test.describe('Simple Mode', () => {
	test('/?mode=simple renders simplified layout', async ({ page }) => {
		await page.goto('/?mode=simple');
		// Should have CUT and DISSOLVE buttons (simple mode)
		await expect(page.locator('text=CUT')).toBeVisible();
		await expect(page.locator('text=DISSOLVE')).toBeVisible();
		await expect(page.locator('text=SwitchFrame')).toBeVisible();
	});

	test('simple mode hides audio mixer and bus rows', async ({ page }) => {
		await page.goto('/?mode=simple');
		// Audio mixer and bus rows should NOT be visible in simple mode
		await expect(page.locator('.audio-section')).not.toBeVisible();
		await expect(page.locator('text=PREVIEW BUS')).not.toBeVisible();
		await expect(page.locator('text=PROGRAM BUS')).not.toBeVisible();
	});

	test('gear icon switches back to traditional mode', async ({ page }) => {
		await page.goto('/?mode=simple');
		await expect(page.locator('text=CUT')).toBeVisible();
		// Click gear icon to switch to traditional
		await page.locator('[title="Switch to traditional mode"]').click();
		// Traditional mode elements should now be visible
		await expect(page.locator('.control-room')).toBeVisible();
	});

	test('layout persists across page reloads', async ({ page }) => {
		// Set simple mode via URL
		await page.goto('/?mode=simple');
		await expect(page.locator('text=DISSOLVE')).toBeVisible();
		// Reload without the URL param -- should still be simple from localStorage
		await page.goto('/');
		await expect(page.locator('text=DISSOLVE')).toBeVisible();
	});

	test('traditional mode is default', async ({ page }) => {
		// Clear localStorage first
		await page.goto('/');
		await page.evaluate(() => localStorage.clear());
		await page.reload();
		// Should show traditional layout
		await expect(page.locator('.control-room')).toBeVisible();
	});

	test('MODE button in traditional header switches to simple', async ({ page }) => {
		await page.goto('/');
		await page.evaluate(() => localStorage.clear());
		await page.reload();
		// Click MODE button
		const modeBtn = page.locator('[title="Switch layout mode"]');
		await expect(modeBtn).toBeVisible();
		await modeBtn.click();
		await expect(page.locator('text=DISSOLVE')).toBeVisible();
	});
});

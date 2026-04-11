import { test, expect } from '@playwright/test';

test.describe('On-Air Controller Mode', () => {
	test('renders on-air controller layout', async ({ page }) => {
		await page.goto('/?mode=on-air');
		await expect(page.locator('.on-air-controller')).toBeVisible();
	});

	test('shows status bar', async ({ page }) => {
		await page.goto('/?mode=on-air');
		await expect(page.locator('.oa-status-bar')).toBeVisible();
	});

	test('mode parameter persists', async ({ page }) => {
		await page.goto('/?mode=on-air');
		// Verify the on-air controller is rendered, not traditional or simple
		await expect(page.locator('.on-air-controller')).toBeVisible();
		await expect(page.locator('.control-room')).not.toBeVisible();
	});

	test('layout persists across page reloads', async ({ page }) => {
		await page.goto('/?mode=on-air');
		await expect(page.locator('.on-air-controller')).toBeVisible();
		// Reload without the URL param -- should still be on-air from localStorage
		await page.goto('/');
		await expect(page.locator('.on-air-controller')).toBeVisible();
	});
});

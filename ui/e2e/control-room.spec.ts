import { test, expect } from '@playwright/test';

test('control room page loads', async ({ page }) => {
	await page.goto('/');
	await expect(page.locator('body')).toBeVisible();
});

test('keyboard overlay opens on ? and closes on Escape', async ({ page }) => {
	await page.goto('/');
	// Ensure the page is focused and the keyboard handler has attached
	await page.locator('body').click();
	// Press ? to open overlay (Shift+Slash = ?)
	await page.keyboard.press('Shift+Slash');
	await expect(page.locator('[role="dialog"]')).toBeVisible();
	// Press Escape to close
	await page.keyboard.press('Escape');
	await expect(page.locator('[role="dialog"]')).not.toBeVisible();
});

import { test, expect } from '@playwright/test';

test('control room page loads', async ({ page }) => {
	await page.goto('/');
	await expect(page.locator('body')).toBeVisible();
});

test('control strip contains buses and transitions', async ({ page }) => {
	await page.goto('/');
	const strip = page.locator('.control-strip');
	await expect(strip).toBeVisible();
	// Buses should be inside the control strip
	await expect(strip.locator('.preview-bus')).toBeVisible();
	await expect(strip.locator('.program-bus')).toBeVisible();
});

test('replay panel has mark and transport controls', async ({ page }) => {
	await page.goto('/');
	// Replay is behind the "Replay" tab in BottomTabs — dispatchEvent
	// bypasses loading/disconnect overlays that block pointer events
	await page.getByRole('tab', { name: 'Replay' }).dispatchEvent('click');
	const replayPanel = page.locator('.replay-panel');
	await expect(replayPanel).toBeVisible();
	await expect(replayPanel.locator('.mark-btn.mark-in')).toBeVisible();
	await expect(replayPanel.locator('.mark-btn.mark-out')).toBeVisible();
	// Transport button (play or stop) should be visible
	await expect(replayPanel.locator('.transport-btn')).toBeVisible();
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

import { test, expect } from '@playwright/test';

test('control room page loads', async ({ page }) => {
	await page.goto('/');
	await expect(page.locator('body')).toBeVisible();
});

test('control strip contains source buttons and transitions', async ({ page }) => {
	await page.goto('/');
	const strip = page.locator('.control-strip');
	await expect(strip).toBeVisible();
	// Source buttons container exists (hidden when no sources connected)
	await expect(strip.locator('.source-buttons')).toBeAttached();
	// Transition controls should be visible
	await expect(strip.locator('.btn.cut')).toBeVisible();
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
	// At least one transport button should be visible
	await expect(replayPanel.locator('.t-btn').first()).toBeVisible();
});

test('keyboard overlay opens on / and closes on Escape', async ({ page }) => {
	await page.goto('/');
	// Wait for the control room to render (ensures keyboard handler is attached)
	await expect(page.locator('.control-strip')).toBeVisible();
	// dispatchEvent bypasses loading/disconnect overlays that block pointer events
	await page.locator('body').dispatchEvent('click');
	// Press / to open overlay (handler matches e.code === 'Slash')
	await page.keyboard.press('/');
	await expect(page.locator('[role="dialog"]')).toBeVisible();
	// Press Escape to close
	await page.keyboard.press('Escape');
	await expect(page.locator('[role="dialog"]')).not.toBeVisible();
});

import { test, expect } from '@playwright/test';

test('rapid keyboard cuts do not crash UI', async ({ page }) => {
	await page.goto('/');
	await page.locator('body').click();

	// 20 Space keypresses in rapid succession (Space = Cut)
	for (let i = 0; i < 20; i++) {
		await page.keyboard.press('Space');
	}

	// Page still functional — body visible and no crash overlay
	await expect(page.locator('body')).toBeVisible();
	// Verify no uncaught error crashed the page into the ErrorBoundary
	await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();
});

test('rapid source key presses do not crash UI', async ({ page }) => {
	await page.goto('/');
	await page.locator('body').click();

	// Cycle through source keys 1-8 rapidly, then fire cuts
	for (let round = 0; round < 3; round++) {
		for (let key = 1; key <= 8; key++) {
			await page.keyboard.press(`Digit${key}`);
		}
		await page.keyboard.press('Space');
	}

	await expect(page.locator('body')).toBeVisible();
	await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();
});

test('toast notification flood stays manageable', async ({ page }) => {
	await page.goto('/');

	// Trigger 30 toast notifications by injecting errors via the notify function
	await page.evaluate(() => {
		// Access the notify module — it's imported in the app bundle
		// We simulate errors by dispatching fetch failures on API endpoints
		for (let i = 0; i < 30; i++) {
			fetch('/api/switch/cut', { method: 'POST', body: '{}' }).catch(() => {});
		}
	});

	// Wait a moment for any toasts to render
	await page.waitForTimeout(500);

	// The toast system caps at MAX_VISIBLE=20, so DOM should not be unbounded
	const toasts = page.locator('.toast-item');
	const toastCount = await toasts.count();
	expect(toastCount).toBeLessThanOrEqual(20);

	// Page still functional
	await expect(page.locator('body')).toBeVisible();
	await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();
});

test('rapid layout mode toggles do not crash', async ({ page }) => {
	// Start in traditional mode
	await page.goto('/?mode=traditional');
	await expect(page.locator('body')).toBeVisible();

	// 10 rapid mode switches between traditional and simple
	for (let i = 0; i < 5; i++) {
		await page.goto('/?mode=simple');
		await expect(page.locator('body')).toBeVisible();
		await page.goto('/?mode=traditional');
		await expect(page.locator('body')).toBeVisible();
	}

	// Final check — page renders in traditional mode without crash
	await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();
});

test('page survives simulated API failures', async ({ page }) => {
	await page.goto('/');
	await expect(page.locator('body')).toBeVisible();

	// Intercept all API calls and return 500 errors
	await page.route('/api/**', (route) => {
		return route.fulfill({
			status: 500,
			contentType: 'application/json',
			body: JSON.stringify({ error: 'simulated server error' }),
		});
	});

	// Trigger various API interactions that will all fail
	await page.locator('body').click();
	await page.keyboard.press('Space'); // Cut
	await page.keyboard.press('Digit1'); // Preview source 1
	await page.keyboard.press('Digit2'); // Preview source 2
	await page.keyboard.press('Space'); // Cut again

	// Let error handling settle
	await page.waitForTimeout(500);

	// Remove the intercept — API "recovers"
	await page.unroute('/api/**');

	// Page should still be alive and interactive
	await expect(page.locator('body')).toBeVisible();
	await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();

	// Verify keyboard handler still works by opening the overlay
	await page.keyboard.press('Shift+Slash');
	await expect(page.locator('[role="dialog"]')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(page.locator('[role="dialog"]')).not.toBeVisible();
});

test('rapid keyboard overlay toggle does not crash', async ({ page }) => {
	await page.goto('/');
	await page.locator('body').click();

	// Rapidly toggle the keyboard overlay 20 times
	for (let i = 0; i < 20; i++) {
		await page.keyboard.press('Shift+Slash'); // open
		await page.keyboard.press('Escape'); // close
	}

	// Page still functional
	await expect(page.locator('body')).toBeVisible();
	await expect(page.locator('.error-boundary-overlay')).not.toBeVisible();
});

import { test, expect } from '@playwright/test';

test.describe('Transition Controls', () => {
	test('renders CUT, AUTO, and FTB buttons', async ({ page }) => {
		await page.goto('/');
		await expect(page.locator('.btn.cut')).toBeVisible();
		await expect(page.locator('.btn.auto')).toBeVisible();
		await expect(page.locator('.btn.ftb')).toBeVisible();
	});

	test('AUTO button is disabled when no preview source', async ({ page }) => {
		await page.goto('/');
		// Without a server, no sources exist → AUTO should be disabled
		const autoBtn = page.locator('.btn.auto');
		await expect(autoBtn).toBeDisabled();
	});

	test('FTB button is visible', async ({ page }) => {
		await page.goto('/');
		const ftbBtn = page.locator('.btn.ftb');
		await expect(ftbBtn).toBeVisible();
	});

	test('transition scrubber is rendered', async ({ page }) => {
		await page.goto('/');
		const scrubber = page.locator('.scrubber');
		await expect(scrubber).toBeVisible();
	});

	test('transition type selector is rendered', async ({ page }) => {
		await page.goto('/');
		await expect(page.getByText('Mix')).toBeVisible();
		await expect(page.getByText('Dip')).toBeVisible();
	});

	test('duration selector is rendered', async ({ page }) => {
		await page.goto('/');
		const select = page.locator('.duration-select');
		await expect(select).toBeVisible();
	});

	test('CUT AUTO FTB button text is visible', async ({ page }) => {
		await page.goto('/');
		await expect(page.getByRole('button', { name: 'CUT' })).toBeVisible();
		await expect(page.getByRole('button', { name: 'AUTO' })).toBeVisible();
		await expect(page.getByRole('button', { name: 'FTB' })).toBeVisible();
	});
});

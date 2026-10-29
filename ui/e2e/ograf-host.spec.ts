import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// The OGraf host template and the adapter the CEF sidecar injects into every
// page, driven the way the sidecar drives them: the adapter goes in after the
// page's load event, and the sidecar reads the detected format, or a
// failure, from the title.

const hostURL = new URL('../../cef-sidecar/demo-templates/ograf-host/index.html', import.meta.url).href;
const adapterJS = readFileSync(
	fileURLToPath(new URL('../../cef-sidecar/resources/adapter.js', import.meta.url)),
	'utf8',
);
const FORMAT = 'switchframe-format:';
const ERROR = 'switchframe-error:ograf-host: ';

const manifest = 'https://graphics.test/headline/headline.ograf.json';

// Records every call the host makes on the graphic.
const graphicModule = `
window.__calls = [];
export default class Graphic extends HTMLElement {
	async load(p) { window.__calls.push('load:' + p.renderType); return { statusCode: 200 }; }
	async playAction() { window.__calls.push('play'); return { statusCode: 200 }; }
	async stopAction() { window.__calls.push('stop'); return { statusCode: 200 }; }
	async updateAction(p) { window.__calls.push('update:' + JSON.stringify(p.data)); return { statusCode: 200 }; }
	async customAction(p) { window.__calls.push('custom:' + p.id); return { statusCode: 200 }; }
}`;

async function serveGraphic(page: Page, delayMs = 0, module = graphicModule): Promise<string[]> {
	const requested: string[] = [];
	await page.route(/^https?:\/\/graphics\.test\//, async (route) => {
		const url = route.request().url();
		requested.push(url);
		if (delayMs) await new Promise((r) => setTimeout(r, delayMs));
		const headers = { 'Access-Control-Allow-Origin': '*' };
		if (url.endsWith('.ograf.json')) {
			await route.fulfill({ headers, contentType: 'application/json', body: JSON.stringify({ name: 'Headline', main: 'graphic.mjs' }) });
		} else if (url.endsWith('/graphic.mjs')) {
			await route.fulfill({ headers, contentType: 'text/javascript', body: module });
		} else {
			await route.fulfill({ status: 404 });
		}
	});
	return requested;
}

// Every title the page sets, in order: the sidecar reports each one, so an
// error title is seen even when the format check overwrites it later.
async function recordTitles(page: Page) {
	await page.addInitScript(() => {
		const desc = Object.getOwnPropertyDescriptor(Document.prototype, 'title')!;
		(window as any).__titles = [];
		Object.defineProperty(Document.prototype, 'title', {
			get() { return desc.get!.call(this); },
			set(v: string) { (window as any).__titles.push(v); desc.set!.call(this, v); },
		});
	});
}

async function titles(page: Page): Promise<string[]> {
	return page.evaluate(() => (window as any).__titles);
}

// What the sidecar runs at OnLoadEnd.
async function injectAdapter(page: Page) {
	await page.evaluate(adapterJS);
	await page.evaluate(`setTimeout(function() {
		var fmt = window.__switchframe ? window.__switchframe.format : 'unknown';
		document.title = '${FORMAT}' + fmt;
	}, 200);`);
}

test.describe('OGraf host template', () => {
	test('loads the graphic the manifest names and hands it to the adapter', async ({ page }) => {
		const requested = await serveGraphic(page);
		await page.goto(`${hostURL}?manifest=${encodeURIComponent(manifest)}`);
		await injectAdapter(page);

		await expect(page).toHaveTitle(FORMAT + 'ograf');
		expect(requested).toEqual([manifest, 'https://graphics.test/headline/graphic.mjs']);

		await page.evaluate(() => (window as any).__switchframe.update('{"_headlineText":"Hello"}'));
		await page.evaluate(() => (window as any).__switchframe.play());
		await page.evaluate(() => (window as any).__switchframe.stop());
		await expect.poll(() => page.evaluate(() => (window as any).__calls)).toEqual([
			'load:realtime',
			'update:{"_headlineText":"Hello"}',
			'play',
			'stop',
		]);
	});

	// A graphic slower than the sidecar's format check must not be reported as
	// OGraf before it can play, and must be reported once it can: that second
	// report is what makes the engine send the graphic its data.
	test('reports OGraf only once a slow graphic has loaded', async ({ page }) => {
		await serveGraphic(page, 1500);
		await page.goto(`${hostURL}?manifest=${encodeURIComponent(manifest)}`);
		await injectAdapter(page);

		await expect(page).toHaveTitle(FORMAT + 'unknown');
		await expect(page).toHaveTitle(FORMAT + 'ograf', { timeout: 10_000 });
		await page.evaluate(() => (window as any).__switchframe.play());
		await expect.poll(() => page.evaluate(() => (window as any).__calls)).toEqual(['load:realtime', 'play']);
	});

	// The reason in the reported error tells the module's own refusal apart
	// from Chromium refusing the fetch.
	for (const [name, url] of [
		['an http manifest', 'http://graphics.test/headline/headline.ograf.json'],
		['a file manifest', 'file:///etc/passwd'],
	]) {
		test(`refuses ${name}`, async ({ page }) => {
			await recordTitles(page);
			const requested = await serveGraphic(page);
			await page.goto(`${hostURL}?manifest=${encodeURIComponent(url)}`);
			await injectAdapter(page);

			await expect.poll(() => titles(page)).toContain(ERROR + 'not an https URL: ' + url);
			expect(requested).toEqual([]);
			expect(await page.locator('ograf-host-graphic').count()).toBe(0);
		});
	}

	test('reports a missing manifest', async ({ page }) => {
		await recordTitles(page);
		await page.goto(hostURL);
		await injectAdapter(page);

		await expect.poll(() => titles(page)).toContain(ERROR + 'no ?manifest=<https URL of an OGraf manifest> given');
	});

	test('reports a graphic whose load fails', async ({ page }) => {
		await recordTitles(page);
		await serveGraphic(page, 0, `
export default class Graphic extends HTMLElement {
	async load() { return { statusCode: 500, statusMessage: 'no data source' }; }
}`);
		await page.goto(`${hostURL}?manifest=${encodeURIComponent(manifest)}`);
		await injectAdapter(page);

		await expect.poll(() => titles(page)).toContain(ERROR + 'load returned status 500: no data source');
		expect(await titles(page)).not.toContain(FORMAT + 'ograf');
	});

	// A module that registers its own class still loads: the host defines a
	// subclass, since one constructor cannot be defined under two tags.
	test('hosts a graphic that defines its own element', async ({ page }) => {
		await serveGraphic(page, 0, graphicModule + `
customElements.define('headline-graphic', Graphic);`);
		await page.goto(`${hostURL}?manifest=${encodeURIComponent(manifest)}`);
		await injectAdapter(page);

		await expect(page).toHaveTitle(FORMAT + 'ograf');
		await page.evaluate(() => (window as any).__switchframe.play());
		await expect.poll(() => page.evaluate(() => (window as any).__calls)).toEqual(['load:realtime', 'play']);
	});
});

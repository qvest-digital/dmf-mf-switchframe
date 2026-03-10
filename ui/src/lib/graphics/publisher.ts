/**
 * GraphicsPublisher renders a template to an offscreen canvas, extracts
 * RGBA pixel data, and sends it to the server via REST POST.
 *
 * Before publishing, it queries the server for the program video resolution
 * and renders at that size so the compositor can composite without scaling.
 */
import type { GraphicsTemplate } from './templates';
import { resolveApiUrl } from '$lib/api/base-url';
import { authHeaders } from '$lib/api/switch-api';

/** Encode a Uint8Array/Uint8ClampedArray to a base64 string. */
function uint8ArrayToBase64(bytes: Uint8Array | Uint8ClampedArray): string {
	let binary = '';
	for (let i = 0; i < bytes.length; i++) {
		binary += String.fromCharCode(bytes[i]);
	}
	return btoa(binary);
}

export class GraphicsPublisher {
	private canvas: OffscreenCanvas | null = null;
	private ctx: OffscreenCanvasRenderingContext2D | null = null;
	private width = 0;
	private height = 0;

	/**
	 * Ensure the internal canvas matches the given resolution.
	 * Re-creates canvas only when dimensions change.
	 */
	private ensureCanvas(width: number, height: number): void {
		if (this.width === width && this.height === height && this.canvas) return;
		this.width = width;
		this.height = height;
		this.canvas = new OffscreenCanvas(width, height);
		this.ctx = this.canvas.getContext('2d', { willReadFrequently: true })!;
	}

	/**
	 * Render a template and upload the resulting RGBA frame to a specific layer.
	 * Queries the graphics status endpoint to learn the program resolution
	 * and renders at that size.
	 */
	async publish(layerId: number, template: GraphicsTemplate, values: Record<string, string>): Promise<void> {
		// Query program resolution from compositor status.
		const statusRes = await fetch(resolveApiUrl('/api/graphics'), {
			headers: authHeaders(),
		});
		if (!statusRes.ok) {
			throw new Error(`Failed to get graphics status: HTTP ${statusRes.status}`);
		}
		const status = await statusRes.json();
		const w = status.programWidth || 1280;
		const h = status.programHeight || 720;

		this.ensureCanvas(w, h);

		// Clear canvas (fully transparent)
		this.ctx!.clearRect(0, 0, this.width, this.height);

		// Render template at program resolution
		template.render(this.ctx!, this.width, this.height, values);

		// Extract RGBA pixel data
		const imageData = this.ctx!.getImageData(0, 0, this.width, this.height);

		// Encode as base64 — Go's encoding/json decodes []byte from base64.
		const base64 = uint8ArrayToBase64(imageData.data);

		// Upload to server
		const response = await fetch(resolveApiUrl(`/api/graphics/${layerId}/frame`), {
			method: 'POST',
			headers: { 'Content-Type': 'application/json', ...authHeaders() },
			body: JSON.stringify({
				width: this.width,
				height: this.height,
				template: template.id,
				rgba: base64,
			}),
		});

		if (!response.ok) {
			const body = await response.json().catch(() => ({ error: 'unknown error' }));
			throw new Error(body.error || `HTTP ${response.status}`);
		}
	}

	/**
	 * Render a template to a regular Canvas element for preview purposes.
	 * Does not upload to the server.
	 */
	renderPreview(
		canvas: HTMLCanvasElement,
		template: GraphicsTemplate,
		values: Record<string, string>,
	): void {
		const ctx = canvas.getContext('2d');
		if (!ctx) return;

		// Clear with checkerboard to show transparency
		const size = 8;
		for (let y = 0; y < canvas.height; y += size) {
			for (let x = 0; x < canvas.width; x += size) {
				ctx.fillStyle = ((x / size + y / size) % 2 === 0) ? '#2a2a2a' : '#1a1a1a';
				ctx.fillRect(x, y, size, size);
			}
		}

		// Render template on top
		template.render(ctx, canvas.width, canvas.height, values);
	}

	destroy(): void {
		this.canvas = null;
		this.ctx = null;
	}
}

/**
 * GraphicsPublisher renders a template to an offscreen canvas, extracts
 * RGBA pixel data, and sends it to the server via REST POST.
 */
import type { GraphicsTemplate } from './templates';

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
	 * Initialize the publisher with the target resolution.
	 * This should match the program output resolution.
	 */
	init(width: number, height: number): void {
		this.width = width;
		this.height = height;
		this.canvas = new OffscreenCanvas(width, height);
		this.ctx = this.canvas.getContext('2d', { willReadFrequently: true })!;
	}

	/**
	 * Render a template and upload the resulting RGBA frame to the server.
	 *
	 * @param template - The template to render.
	 * @param values - Field values for the template.
	 * @returns Promise that resolves when the upload is complete.
	 */
	async publish(template: GraphicsTemplate, values: Record<string, string>): Promise<void> {
		if (!this.canvas || !this.ctx) {
			throw new Error('GraphicsPublisher not initialized. Call init() first.');
		}

		// Clear canvas (fully transparent)
		this.ctx.clearRect(0, 0, this.width, this.height);

		// Render template
		template.render(this.ctx, this.width, this.height, values);

		// Extract RGBA pixel data
		const imageData = this.ctx.getImageData(0, 0, this.width, this.height);

		// Encode as base64 — Go's encoding/json decodes []byte from base64.
		const base64 = uint8ArrayToBase64(imageData.data);

		// Upload to server
		const response = await fetch('/api/graphics/frame', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
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

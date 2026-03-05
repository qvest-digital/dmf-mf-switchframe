/**
 * Graphics template system for the DSK (Downstream Keyer).
 *
 * Templates render to an OffscreenCanvas which produces RGBA pixel data
 * that can be sent to the server for compositing onto program output.
 */

export interface GraphicsTemplate {
	/** Unique template identifier. */
	id: string;
	/** Human-readable display name. */
	name: string;
	/** Template fields that can be edited by the operator. */
	fields: TemplateField[];
	/** Render the template to a canvas context at the given resolution. */
	render(ctx: CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D, width: number, height: number, values: Record<string, string>): void;
}

export interface TemplateField {
	/** Field key used in the values record. */
	key: string;
	/** Label shown in the UI. */
	label: string;
	/** Default value. */
	defaultValue: string;
	/** Maximum character count (optional). */
	maxLength?: number;
}

/**
 * Lower-third template: name on top line, title/role on bottom line.
 * Renders as a semi-transparent bar in the lower ~15% of the frame.
 */
export const lowerThirdTemplate: GraphicsTemplate = {
	id: 'lower-third',
	name: 'Lower Third',
	fields: [
		{ key: 'name', label: 'Name', defaultValue: 'John Smith', maxLength: 40 },
		{ key: 'title', label: 'Title', defaultValue: 'Speaker', maxLength: 60 },
	],
	render(ctx, width, height, values) {
		const barHeight = Math.round(height * 0.12);
		const barY = height - barHeight - Math.round(height * 0.05);
		const padding = Math.round(width * 0.03);

		// Semi-transparent background bar
		ctx.fillStyle = 'rgba(0, 0, 0, 0.75)';
		ctx.fillRect(0, barY, width, barHeight);

		// Accent line at top of bar
		ctx.fillStyle = 'rgba(59, 130, 246, 0.9)';
		ctx.fillRect(0, barY, width, 3);

		// Name text
		const nameFontSize = Math.round(barHeight * 0.42);
		ctx.font = `bold ${nameFontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.textBaseline = 'top';
		ctx.fillText(values.name || '', padding, barY + Math.round(barHeight * 0.12));

		// Title text
		const titleFontSize = Math.round(barHeight * 0.30);
		ctx.font = `${titleFontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = 'rgba(200, 200, 200, 0.9)';
		ctx.fillText(values.title || '', padding, barY + Math.round(barHeight * 0.58));
	},
};

/**
 * Full-screen card template: centered text on a semi-transparent background.
 * Used for title cards, scripture references, announcements, etc.
 */
export const fullScreenCardTemplate: GraphicsTemplate = {
	id: 'full-screen',
	name: 'Full Screen Card',
	fields: [
		{ key: 'heading', label: 'Heading', defaultValue: 'Welcome', maxLength: 60 },
		{ key: 'body', label: 'Body', defaultValue: '', maxLength: 200 },
	],
	render(ctx, width, height, values) {
		// Semi-transparent full-screen background
		ctx.fillStyle = 'rgba(0, 0, 0, 0.80)';
		ctx.fillRect(0, 0, width, height);

		// Heading
		const headingSize = Math.round(height * 0.06);
		ctx.font = `bold ${headingSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.textAlign = 'center';
		ctx.textBaseline = 'middle';
		ctx.fillText(values.heading || '', width / 2, height * 0.42);

		// Body text
		if (values.body) {
			const bodySize = Math.round(height * 0.035);
			ctx.font = `${bodySize}px -apple-system, "Segoe UI", sans-serif`;
			ctx.fillStyle = 'rgba(220, 220, 220, 0.95)';
			ctx.fillText(values.body, width / 2, height * 0.55);
		}

		// Reset alignment
		ctx.textAlign = 'start';
	},
};

/**
 * Scrolling ticker template: text bar at the bottom of the frame.
 * Note: For MVP, this renders as a static bar. Scrolling animation
 * would require frame-by-frame rendering from the browser.
 */
export const tickerTemplate: GraphicsTemplate = {
	id: 'ticker',
	name: 'Ticker',
	fields: [
		{ key: 'text', label: 'Ticker Text', defaultValue: 'Breaking News: Welcome to Switchframe', maxLength: 200 },
	],
	render(ctx, width, height, values) {
		const barHeight = Math.round(height * 0.06);
		const barY = height - barHeight;

		// Background bar
		ctx.fillStyle = 'rgba(20, 20, 60, 0.90)';
		ctx.fillRect(0, barY, width, barHeight);

		// Top border
		ctx.fillStyle = 'rgba(59, 130, 246, 1.0)';
		ctx.fillRect(0, barY, width, 2);

		// Ticker text
		const fontSize = Math.round(barHeight * 0.55);
		ctx.font = `${fontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.textBaseline = 'middle';
		ctx.fillText(values.text || '', Math.round(width * 0.02), barY + barHeight / 2);
	},
};

/** All built-in templates indexed by ID. */
export const builtinTemplates: Record<string, GraphicsTemplate> = {
	'lower-third': lowerThirdTemplate,
	'full-screen': fullScreenCardTemplate,
	'ticker': tickerTemplate,
};

/** Ordered list of built-in templates for UI display. */
export const templateList: GraphicsTemplate[] = [
	lowerThirdTemplate,
	fullScreenCardTemplate,
	tickerTemplate,
];

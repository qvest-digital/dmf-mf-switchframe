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
	/** Whether this template supports animation (controls ANIMATE button in GraphicsPanel). */
	supportsAnimation?: boolean;
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

/**
 * CNN/MSNBC-style news lower third: red name bar over dark charcoal title bar.
 * Two-tone bar with accent stripe and left tag block.
 */
export const newsLowerThirdTemplate: GraphicsTemplate = {
	id: 'news-lower-third',
	name: 'News Lower Third',
	fields: [
		{ key: 'name', label: 'Name', defaultValue: 'Jane Doe', maxLength: 40 },
		{ key: 'title', label: 'Title', defaultValue: 'Senior Correspondent', maxLength: 60 },
	],
	render(ctx, width, height, values) {
		ctx.save();

		const barHeight = Math.round(height * 0.15);
		const barY = height - barHeight - Math.round(height * 0.05);
		const topHeight = Math.round(barHeight * 0.55);
		const bottomHeight = barHeight - topHeight;
		const stripeHeight = 4;
		const tagWidth = Math.round(width * 0.042); // ~80px at 1920
		const padding = Math.round(width * 0.03);

		// Top section: red bar
		ctx.globalAlpha = 0.92;
		ctx.fillStyle = '#CC0000';
		ctx.fillRect(0, barY, width, topHeight);

		// Left accent tag block (solid red, slightly darker)
		ctx.fillStyle = '#AA0000';
		ctx.fillRect(0, barY, tagWidth, topHeight);

		// Name text on top bar
		const nameFontSize = Math.round(topHeight * 0.55);
		ctx.globalAlpha = 1;
		ctx.font = `bold ${nameFontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.textBaseline = 'middle';
		ctx.fillText(values.name || '', tagWidth + padding, barY + topHeight / 2);

		// Bright red accent stripe between sections
		ctx.globalAlpha = 0.92;
		ctx.fillStyle = '#FF1A1A';
		ctx.fillRect(0, barY + topHeight, width, stripeHeight);

		// Bottom section: dark charcoal
		ctx.fillStyle = '#1A1A1A';
		ctx.fillRect(0, barY + topHeight + stripeHeight, width, bottomHeight - stripeHeight);

		// Left accent tag block on bottom
		ctx.fillStyle = '#CC0000';
		ctx.fillRect(0, barY + topHeight + stripeHeight, tagWidth, bottomHeight - stripeHeight);

		// Title text on bottom bar
		const titleFontSize = Math.round((bottomHeight - stripeHeight) * 0.55);
		ctx.globalAlpha = 1;
		ctx.font = `${titleFontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = 'rgba(220, 220, 220, 0.95)';
		ctx.textBaseline = 'middle';
		ctx.fillText(values.title || '', tagWidth + padding, barY + topHeight + stripeHeight + (bottomHeight - stripeHeight) / 2);

		ctx.restore();
	},
};

/**
 * Network bug: translucent station identifier in the top-right corner
 * with a small "LIVE" indicator below.
 */
export const networkBugTemplate: GraphicsTemplate = {
	id: 'network-bug',
	name: 'Network Bug',
	supportsAnimation: true,
	fields: [
		{ key: 'text', label: 'Bug Text', defaultValue: 'SF', maxLength: 10 },
	],
	render(ctx, width, height, values) {
		ctx.save();

		const marginX = Math.round(width * 0.08);
		const marginY = Math.round(height * 0.08);
		const bugX = width - marginX;
		const bugY = marginY;

		// Bold stylized bug text
		const bugFontSize = Math.round(height * 0.05);
		ctx.globalAlpha = 0.6;
		ctx.font = `900 ${bugFontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.textAlign = 'center';
		ctx.textBaseline = 'top';
		const bugTextWidth = ctx.measureText(values.text || 'SF').width;
		const bugCenterX = bugX - bugTextWidth / 2;
		ctx.fillText(values.text || 'SF', bugCenterX, bugY);

		// "LIVE" indicator below: red dot + text, centered under bug text
		const liveY = bugY + bugFontSize + Math.round(height * 0.01);
		const liveFontSize = Math.round(bugFontSize * 0.35);
		const dotRadius = Math.round(liveFontSize * 0.4);

		ctx.globalAlpha = 0.5;

		// Measure LIVE text to center the dot+text group
		ctx.font = `bold ${liveFontSize}px -apple-system, "Segoe UI", sans-serif`;
		const liveTextWidth = ctx.measureText('LIVE').width;
		const liveGroupWidth = dotRadius * 2 + liveFontSize * 0.4 + liveTextWidth;
		const liveGroupLeft = bugCenterX - liveGroupWidth / 2;

		// Red dot
		ctx.fillStyle = '#FF0000';
		ctx.beginPath();
		ctx.arc(liveGroupLeft + dotRadius, liveY + liveFontSize * 0.5, dotRadius, 0, Math.PI * 2);
		ctx.fill();

		// "LIVE" text
		ctx.fillStyle = '#ffffff';
		ctx.textAlign = 'left';
		ctx.textBaseline = 'top';
		ctx.fillText('LIVE', liveGroupLeft + dotRadius * 2 + liveFontSize * 0.4, liveY);

		ctx.restore();
	},
};

/**
 * Sports score bug: compact horizontal bar in the top-left corner showing
 * home/away teams, scores, period, and game clock.
 */
export const scoreBugTemplate: GraphicsTemplate = {
	id: 'score-bug',
	name: 'Score Bug',
	fields: [
		{ key: 'home', label: 'Home Team', defaultValue: 'HOME', maxLength: 20 },
		{ key: 'away', label: 'Away Team', defaultValue: 'AWAY', maxLength: 20 },
		{ key: 'homeScore', label: 'Home Score', defaultValue: '0', maxLength: 3 },
		{ key: 'awayScore', label: 'Away Score', defaultValue: '0', maxLength: 3 },
		{ key: 'period', label: 'Period', defaultValue: '1ST', maxLength: 5 },
		{ key: 'clock', label: 'Clock', defaultValue: '12:00', maxLength: 8 },
	],
	render(ctx, width, height, values) {
		ctx.save();

		const barHeight = Math.round(height * 0.05);
		const barWidth = Math.round(width * 0.45);
		const barX = Math.round(width * 0.03);
		const barY = Math.round(height * 0.03);
		const padding = Math.round(barWidth * 0.02);

		// Semi-transparent background
		ctx.globalAlpha = 0.85;
		ctx.fillStyle = '#000000';
		ctx.fillRect(barX, barY, barWidth, barHeight);

		ctx.globalAlpha = 1;

		const teamFontSize = Math.round(barHeight * 0.45);
		const scoreFontSize = Math.round(barHeight * 0.50);
		const infoFontSize = Math.round(barHeight * 0.38);

		// Layout: |  HOME  score | divider | AWAY  score | period . clock  |
		const sectionWidth = Math.round(barWidth / 3);
		const centerY = barY + barHeight / 2;

		// Home team name (bold) + score
		ctx.textBaseline = 'middle';
		ctx.textAlign = 'start';
		ctx.font = `bold ${teamFontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.fillText(values.home || 'HOME', barX + padding, centerY);

		// Home score (monospace)
		ctx.textAlign = 'end';
		ctx.font = `bold ${scoreFontSize}px "SF Mono", "Cascadia Code", "Consolas", monospace`;
		ctx.fillText(values.homeScore || '0', barX + sectionWidth - padding, centerY);

		// Divider line
		ctx.fillStyle = '#CC0000';
		ctx.fillRect(barX + sectionWidth, barY + Math.round(barHeight * 0.15), 2, Math.round(barHeight * 0.7));

		// Away team name (bold) + score
		ctx.textAlign = 'start';
		ctx.font = `bold ${teamFontSize}px -apple-system, "Segoe UI", sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.fillText(values.away || 'AWAY', barX + sectionWidth + padding + 2, centerY);

		// Away score (monospace)
		ctx.textAlign = 'end';
		ctx.font = `bold ${scoreFontSize}px "SF Mono", "Cascadia Code", "Consolas", monospace`;
		ctx.fillText(values.awayScore || '0', barX + sectionWidth * 2 - padding, centerY);

		// Divider line
		ctx.fillStyle = '#CC0000';
		ctx.fillRect(barX + sectionWidth * 2, barY + Math.round(barHeight * 0.15), 2, Math.round(barHeight * 0.7));

		// Period and clock
		ctx.textAlign = 'center';
		ctx.font = `bold ${infoFontSize}px "SF Mono", "Cascadia Code", "Consolas", monospace`;
		ctx.fillStyle = 'rgba(220, 220, 220, 0.95)';
		const infoCenter = barX + sectionWidth * 2 + sectionWidth / 2 + 2;
		const periodClock = `${values.period || '1ST'} ${values.clock || '12:00'}`;
		ctx.fillText(periodClock, infoCenter, centerY);

		ctx.restore();
	},
};

/** All built-in templates indexed by ID. */
export const builtinTemplates: Record<string, GraphicsTemplate> = {
	'lower-third': lowerThirdTemplate,
	'full-screen': fullScreenCardTemplate,
	'ticker': tickerTemplate,
	'news-lower-third': newsLowerThirdTemplate,
	'network-bug': networkBugTemplate,
	'score-bug': scoreBugTemplate,
};

/** Ordered list of built-in templates for UI display. */
export const templateList: GraphicsTemplate[] = [
	lowerThirdTemplate,
	fullScreenCardTemplate,
	tickerTemplate,
	newsLowerThirdTemplate,
	networkBugTemplate,
	scoreBugTemplate,
];

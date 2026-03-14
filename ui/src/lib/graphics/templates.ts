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
	supportsAnimation: true,
	fields: [
		{ key: 'name', label: 'Name', defaultValue: 'John Smith', maxLength: 40 },
		{ key: 'title', label: 'Title', defaultValue: 'Speaker', maxLength: 60 },
	],
	render(ctx, width, height, values) {
		ctx.save();

		const barHeight = Math.round(height * 0.13);
		const barY = height - barHeight - Math.round(height * 0.05);
		const accentW = Math.round(width * 0.008);
		const padding = Math.round(width * 0.025);
		const radius = Math.round(height * 0.008);

		// Gradient blue accent block (left edge)
		const accentBlockW = Math.round(width * 0.045);
		const accentGrad = ctx.createLinearGradient(0, barY, 0, barY + barHeight);
		accentGrad.addColorStop(0, '#2563eb');
		accentGrad.addColorStop(1, '#1d4ed8');
		ctx.beginPath();
		ctx.roundRect(0, barY, accentBlockW, barHeight, [radius, 0, 0, radius]);
		ctx.fillStyle = accentGrad;
		ctx.fill();

		// Main bar with gradient (dark to slightly lighter)
		const barGrad = ctx.createLinearGradient(0, barY, width * 0.7, barY);
		barGrad.addColorStop(0, 'rgba(15, 15, 20, 0.92)');
		barGrad.addColorStop(0.7, 'rgba(25, 25, 35, 0.88)');
		barGrad.addColorStop(1, 'rgba(15, 15, 20, 0.0)');
		ctx.beginPath();
		ctx.roundRect(accentBlockW, barY, width * 0.65, barHeight, [0, radius, radius, 0]);
		ctx.fillStyle = barGrad;
		ctx.fill();

		// Drop shadow for text
		ctx.shadowColor = 'rgba(0, 0, 0, 0.6)';
		ctx.shadowBlur = 4;
		ctx.shadowOffsetX = 1;
		ctx.shadowOffsetY = 1;

		// Name text (bold, larger)
		const nameFontSize = Math.round(barHeight * 0.42);
		ctx.font = `700 ${nameFontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.textBaseline = 'top';
		ctx.fillText(values.name || '', accentBlockW + padding, barY + Math.round(barHeight * 0.12));

		// Title text (lighter weight, slightly transparent)
		const titleFontSize = Math.round(barHeight * 0.28);
		ctx.font = `400 ${titleFontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
		ctx.fillStyle = 'rgba(180, 200, 230, 0.9)';
		ctx.fillText(values.title || '', accentBlockW + padding, barY + Math.round(barHeight * 0.60));

		ctx.restore();
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
		ctx.save();

		// Radial gradient background (navy center → black edges)
		const cx = width / 2;
		const cy = height * 0.45;
		const outerRadius = Math.max(width, height) * 0.7;
		const bgGrad = ctx.createRadialGradient(cx, cy, 0, cx, cy, outerRadius);
		bgGrad.addColorStop(0, 'rgba(15, 23, 42, 0.92)');
		bgGrad.addColorStop(0.5, 'rgba(10, 15, 30, 0.94)');
		bgGrad.addColorStop(1, 'rgba(2, 6, 15, 0.96)');
		ctx.fillStyle = bgGrad;
		ctx.fillRect(0, 0, width, height);

		// Vignette overlay (darker edges)
		const vigGrad = ctx.createRadialGradient(cx, cy, outerRadius * 0.3, cx, cy, outerRadius);
		vigGrad.addColorStop(0, 'rgba(0, 0, 0, 0)');
		vigGrad.addColorStop(1, 'rgba(0, 0, 0, 0.4)');
		ctx.fillStyle = vigGrad;
		ctx.fillRect(0, 0, width, height);

		// Decorative horizontal rule with gradient endpoints
		const ruleY = height * 0.50;
		const ruleW = width * 0.35;
		const ruleX = (width - ruleW) / 2;
		const ruleGrad = ctx.createLinearGradient(ruleX, 0, ruleX + ruleW, 0);
		ruleGrad.addColorStop(0, 'rgba(148, 163, 184, 0)');
		ruleGrad.addColorStop(0.2, 'rgba(148, 163, 184, 0.6)');
		ruleGrad.addColorStop(0.5, 'rgba(203, 213, 225, 0.8)');
		ruleGrad.addColorStop(0.8, 'rgba(148, 163, 184, 0.6)');
		ruleGrad.addColorStop(1, 'rgba(148, 163, 184, 0)');
		ctx.fillStyle = ruleGrad;
		ctx.fillRect(ruleX, ruleY, ruleW, 1);

		// Drop shadow for text
		ctx.shadowColor = 'rgba(0, 0, 0, 0.7)';
		ctx.shadowBlur = 6;
		ctx.shadowOffsetY = 2;

		// Heading (serif font for cinematic feel)
		const headingSize = Math.round(height * 0.06);
		ctx.font = `700 ${headingSize}px Georgia, "Times New Roman", serif`;
		ctx.fillStyle = '#f1f5f9';
		ctx.textAlign = 'center';
		ctx.textBaseline = 'middle';
		ctx.fillText(values.heading || '', cx, height * 0.42);

		// Body text (lighter, sans-serif)
		if (values.body) {
			ctx.shadowBlur = 3;
			const bodySize = Math.round(height * 0.032);
			ctx.font = `300 ${bodySize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
			ctx.fillStyle = 'rgba(203, 213, 225, 0.90)';
			ctx.fillText(values.body, cx, height * 0.57);
		}

		ctx.restore();
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
		{ key: 'label', label: 'Label', defaultValue: 'BREAKING', maxLength: 12 },
		{ key: 'text', label: 'Ticker Text', defaultValue: 'Breaking News: Welcome to Switchframe', maxLength: 200 },
	],
	render(ctx, width, height, values) {
		ctx.save();

		const barHeight = Math.round(height * 0.065);
		const barY = height - barHeight;

		// Dark background bar
		const barGrad = ctx.createLinearGradient(0, barY, 0, barY + barHeight);
		barGrad.addColorStop(0, 'rgba(10, 10, 25, 0.94)');
		barGrad.addColorStop(1, 'rgba(5, 5, 18, 0.96)');
		ctx.fillStyle = barGrad;
		ctx.fillRect(0, barY, width, barHeight);

		// Neon-like blue glow top edge
		ctx.shadowColor = 'rgba(59, 130, 246, 0.8)';
		ctx.shadowBlur = 8;
		ctx.shadowOffsetY = 0;
		ctx.fillStyle = '#3b82f6';
		ctx.fillRect(0, barY, width, 2);
		ctx.shadowBlur = 0;

		// Label pill (e.g., "BREAKING" / "LIVE")
		const label = values.label || '';
		let textStartX = Math.round(width * 0.015);
		if (label) {
			const pillFontSize = Math.round(barHeight * 0.42);
			ctx.font = `800 ${pillFontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
			const pillTextW = ctx.measureText(label).width;
			const pillPadH = Math.round(barHeight * 0.15);
			const pillPadW = Math.round(pillFontSize * 0.5);
			const pillH = Math.round(barHeight * 0.6);
			const pillY = barY + (barHeight - pillH) / 2;
			const pillX = textStartX;
			const pillW = pillTextW + pillPadW * 2;
			const pillR = Math.round(pillH * 0.25);

			// Pill background
			const pillGrad = ctx.createLinearGradient(pillX, pillY, pillX, pillY + pillH);
			pillGrad.addColorStop(0, '#2563eb');
			pillGrad.addColorStop(1, '#1d4ed8');
			ctx.beginPath();
			ctx.roundRect(pillX, pillY, pillW, pillH, pillR);
			ctx.fillStyle = pillGrad;
			ctx.fill();

			// Pill text
			ctx.fillStyle = '#ffffff';
			ctx.textBaseline = 'middle';
			ctx.fillText(label, pillX + pillPadW, barY + barHeight / 2);

			// Vertical divider after pill
			const divX = pillX + pillW + Math.round(width * 0.008);
			ctx.fillStyle = 'rgba(59, 130, 246, 0.5)';
			ctx.fillRect(divX, barY + Math.round(barHeight * 0.2), 1, Math.round(barHeight * 0.6));

			textStartX = divX + Math.round(width * 0.008);
		}

		// Ticker text
		const fontSize = Math.round(barHeight * 0.48);
		ctx.font = `500 ${fontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
		ctx.fillStyle = '#e2e8f0';
		ctx.textBaseline = 'middle';
		ctx.fillText(values.text || '', textStartX, barY + barHeight / 2);

		ctx.restore();
	},
};

/**
 * CNN/MSNBC-style news lower third: red name bar over dark charcoal title bar.
 * Two-tone bar with accent stripe and left tag block.
 */
export const newsLowerThirdTemplate: GraphicsTemplate = {
	id: 'news-lower-third',
	name: 'News Lower Third',
	supportsAnimation: true,
	fields: [
		{ key: 'name', label: 'Name', defaultValue: 'Jane Doe', maxLength: 40 },
		{ key: 'title', label: 'Title', defaultValue: 'Senior Correspondent', maxLength: 60 },
	],
	render(ctx, width, height, values) {
		ctx.save();

		const barHeight = Math.round(height * 0.14);
		const barY = height - barHeight - Math.round(height * 0.05);
		const topHeight = Math.round(barHeight * 0.55);
		const bottomHeight = barHeight - topHeight;
		const skew = Math.round(width * 0.012);
		const padding = Math.round(width * 0.03);
		const tagWidth = Math.round(width * 0.065);

		// Angled parallelogram left tag (red accent)
		ctx.beginPath();
		ctx.moveTo(0, barY);
		ctx.lineTo(tagWidth + skew, barY);
		ctx.lineTo(tagWidth, barY + barHeight);
		ctx.lineTo(0, barY + barHeight);
		ctx.closePath();
		const tagGrad = ctx.createLinearGradient(0, barY, 0, barY + barHeight);
		tagGrad.addColorStop(0, '#dc2626');
		tagGrad.addColorStop(1, '#991b1b');
		ctx.fillStyle = tagGrad;
		ctx.fill();

		// Top bar (gradient red with subtle text shadow)
		const topGrad = ctx.createLinearGradient(tagWidth, barY, width * 0.7, barY);
		topGrad.addColorStop(0, 'rgba(185, 28, 28, 0.94)');
		topGrad.addColorStop(0.8, 'rgba(153, 27, 27, 0.90)');
		topGrad.addColorStop(1, 'rgba(127, 29, 29, 0.0)');
		ctx.fillStyle = topGrad;
		ctx.fillRect(tagWidth, barY, width * 0.65, topHeight);

		// Thin bright accent stripe between sections
		ctx.fillStyle = '#ef4444';
		ctx.fillRect(tagWidth, barY + topHeight, width * 0.65, 2);

		// Bottom bar (cooler charcoal with gradient)
		const bottomGrad = ctx.createLinearGradient(tagWidth, 0, width * 0.7, 0);
		bottomGrad.addColorStop(0, 'rgba(24, 24, 30, 0.94)');
		bottomGrad.addColorStop(0.8, 'rgba(30, 30, 38, 0.90)');
		bottomGrad.addColorStop(1, 'rgba(24, 24, 30, 0.0)');
		ctx.fillStyle = bottomGrad;
		ctx.fillRect(tagWidth, barY + topHeight + 2, width * 0.65, bottomHeight - 2);

		// Text shadow
		ctx.shadowColor = 'rgba(0, 0, 0, 0.5)';
		ctx.shadowBlur = 3;
		ctx.shadowOffsetX = 1;
		ctx.shadowOffsetY = 1;

		// Name text on top bar
		const nameFontSize = Math.round(topHeight * 0.52);
		ctx.font = `700 ${nameFontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
		ctx.fillStyle = '#ffffff';
		ctx.textBaseline = 'middle';
		ctx.fillText(values.name || '', tagWidth + padding, barY + topHeight / 2);

		// Title text on bottom bar
		const titleFontSize = Math.round((bottomHeight - 2) * 0.52);
		ctx.font = `400 ${titleFontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
		ctx.fillStyle = 'rgba(210, 210, 220, 0.95)';
		ctx.fillText(values.title || '', tagWidth + padding, barY + topHeight + 2 + (bottomHeight - 2) / 2);

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

		const marginX = Math.round(width * 0.06);
		const marginY = Math.round(height * 0.06);

		// Bug text metrics
		const bugFontSize = Math.round(height * 0.045);
		ctx.font = `900 ${bugFontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
		const bugText = values.text || 'SF';
		const textMetrics = ctx.measureText(bugText);
		const textW = textMetrics.width;

		// Backdrop pill dimensions
		const pillPadX = Math.round(bugFontSize * 0.6);
		const pillPadY = Math.round(bugFontSize * 0.3);
		const pillW = textW + pillPadX * 2;
		const pillH = bugFontSize + pillPadY * 2;
		const pillX = width - marginX - pillW;
		const pillY = marginY;
		const pillR = Math.round(pillH * 0.3);

		// Rounded rect backdrop pill (semi-transparent for contrast)
		ctx.globalAlpha = 0.35;
		ctx.beginPath();
		ctx.roundRect(pillX, pillY, pillW, pillH, pillR);
		ctx.fillStyle = '#000000';
		ctx.fill();

		// Bug text
		ctx.globalAlpha = 0.75;
		ctx.fillStyle = '#ffffff';
		ctx.textAlign = 'center';
		ctx.textBaseline = 'middle';
		ctx.fillText(bugText, pillX + pillW / 2, pillY + pillH / 2);

		// "LIVE" indicator below pill
		const liveY = pillY + pillH + Math.round(height * 0.008);
		const liveFontSize = Math.round(bugFontSize * 0.32);
		const dotRadius = Math.round(liveFontSize * 0.35);

		ctx.font = `700 ${liveFontSize}px "Segoe UI", "Helvetica Neue", Arial, sans-serif`;
		const liveTextW = ctx.measureText('LIVE').width;
		const liveGroupW = dotRadius * 2 + liveFontSize * 0.35 + liveTextW;
		const liveGroupX = pillX + (pillW - liveGroupW) / 2;

		// Red dot (pulsing effect via alpha)
		ctx.globalAlpha = 0.7;
		ctx.fillStyle = '#ef4444';
		ctx.beginPath();
		ctx.arc(liveGroupX + dotRadius, liveY + liveFontSize * 0.5, dotRadius, 0, Math.PI * 2);
		ctx.fill();

		// Glow ring around dot
		ctx.globalAlpha = 0.25;
		ctx.beginPath();
		ctx.arc(liveGroupX + dotRadius, liveY + liveFontSize * 0.5, dotRadius * 1.8, 0, Math.PI * 2);
		ctx.fillStyle = '#ef4444';
		ctx.fill();

		// "LIVE" text
		ctx.globalAlpha = 0.6;
		ctx.fillStyle = '#ffffff';
		ctx.textAlign = 'left';
		ctx.textBaseline = 'top';
		ctx.fillText('LIVE', liveGroupX + dotRadius * 2 + liveFontSize * 0.35, liveY);

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

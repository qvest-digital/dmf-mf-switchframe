<script lang="ts">
	import type { GraphicsLayerState } from '$lib/api/types';
	import type { GraphicsPublisher } from '$lib/graphics/publisher';
	import { templateList, builtinTemplates } from '$lib/graphics/templates';

	interface AnimConfig {
		mode: string;
		minAlpha: number;
		maxAlpha: number;
		speedHz: number;
		toAlpha: number;
		durationMs: number;
	}

	interface FlyConfig {
		direction: string;
		durationMs: number;
	}

	interface TickerStartConfig {
		text: string;
		fontSize: number;
		speed: number;
		bold: boolean;
		loop: boolean;
	}

	interface TextAnimStartConfig {
		mode: string;
		text: string;
		fontSize: number;
		bold: boolean;
		charsPerSec?: number;
		wordDelayMs?: number;
		fadeDurationMs?: number;
	}

	type SourceMode = 'template' | 'image' | 'ticker' | 'textfx';

	interface Props {
		layer: GraphicsLayerState;
		sourceMode: SourceMode;
		templateId: string;
		fields: Record<string, string>;
		animConfig: AnimConfig;
		flyConfig: FlyConfig;
		publisher: GraphicsPublisher;
		onModeChange: (mode: SourceMode) => void;
		onTemplateChange: (tplId: string) => void;
		onFieldChange: (key: string, value: string) => void;
		onAnimConfigChange: (key: string, value: string | number) => void;
		onFlyConfigChange: (key: string, value: string | number) => void;
		onCutOn: () => void;
		onAutoOn: () => void;
		onCutOff: () => void;
		onAutoOff: () => void;
		onFlyIn: () => void;
		onFlyOut: () => void;
		onAnimate: () => void;
		onAnimateStop: () => void;
		onImageUpload: (file: File) => void;
		onImageDelete: () => void;
		onRectChange: (rect: { x: number; y: number; width: number; height: number }) => void;
		onTickerStart: (config: TickerStartConfig) => void;
		onTickerStop: () => void;
		onTickerUpdateText: (text: string) => void;
		tickerActive: boolean;
		onTextAnimStart: (config: TextAnimStartConfig) => void;
		onTextAnimStop: () => void;
		textAnimActive: boolean;
	}

	let {
		layer, sourceMode, templateId, fields, animConfig, flyConfig, publisher,
		onModeChange, onTemplateChange, onFieldChange, onAnimConfigChange, onFlyConfigChange,
		onCutOn, onAutoOn, onCutOff, onAutoOff,
		onFlyIn, onFlyOut, onAnimate, onAnimateStop,
		onImageUpload, onImageDelete, onRectChange,
		onTickerStart, onTickerStop, onTickerUpdateText, tickerActive,
		onTextAnimStart, onTextAnimStop, textAnimActive,
	}: Props = $props();

	let previewCanvas = $state<HTMLCanvasElement | null>(null);
	let flyOpen = $state(false);
	let animOpen = $state(false);

	// Ticker local config
	let tickerText = $state('Breaking News: Welcome to Switchframe');
	let tickerFontSize = $state(32);
	let tickerSpeed = $state(100);
	let tickerBold = $state(false);
	let tickerLoop = $state(true);

	// Text animation local config
	let textAnimMode = $state('typewriter');
	let textAnimText = $state('Hello World');
	let textAnimFontSize = $state(32);
	let textAnimBold = $state(false);
	let textAnimCharsPerSec = $state(15);
	let textAnimWordDelayMs = $state(300);
	let textAnimFadeDurationMs = $state(200);

	let fileInput = $state<HTMLInputElement | null>(null);

	const tpl = $derived(builtinTemplates[templateId]);
	const busy = $derived(
		!!layer.animationMode ||
		(layer.active && layer.fadePosition != null && layer.fadePosition > 0 && layer.fadePosition < 1)
	);

	const tplAccent: Record<string, string> = {
		'lower-third': '#3b82f6',
		'news-lower-third': '#dc2626',
		'full-screen': '#8b5cf6',
		'ticker': '#0891b2',
		'network-bug': '#f59e0b',
		'score-bug': '#16a34a',
	};

	const tplShortName: Record<string, string> = {
		'lower-third': 'Lower 3rd',
		'news-lower-third': 'News L3',
		'full-screen': 'Full Scr',
		'ticker': 'Ticker',
		'network-bug': 'Bug',
		'score-bug': 'Score',
	};

	$effect(() => {
		const canvas = previewCanvas;
		const t = tpl;
		const f = fields;
		if (!t || !canvas) return;
		try {
			publisher.renderPreview(canvas, t, f);
		} catch {
			// Canvas rendering may fail in test environments
		}
	});

	const fieldLayout = $derived.by(() => {
		if (!tpl) return 'single';
		if (tpl.fields.length >= 4) return 'grid-3';
		if (tpl.fields.length >= 2) return 'grid-2';
		return 'single';
	});

	function handleFileSelect(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (file) {
			onImageUpload(file);
			input.value = '';
		}
	}

	const modeLabels: { id: SourceMode; label: string }[] = [
		{ id: 'template', label: 'TEMPLATE' },
		{ id: 'image', label: 'IMAGE' },
		{ id: 'ticker', label: 'TICKER' },
		{ id: 'textfx', label: 'TEXT FX' },
	];
</script>

<div class="detail-pane">
	<!-- Source mode selector -->
	<div class="mode-bar">
		<span class="mode-bar-label">SOURCE</span>
		{#each modeLabels as m}
			<button
				class="mode-btn"
				class:selected={sourceMode === m.id}
				onclick={() => onModeChange(m.id)}
				aria-pressed={sourceMode === m.id}
			>
				{m.label}
				{#if m.id === 'image' && layer.imageName}
					<span class="mode-badge">&#10003;</span>
				{:else if m.id === 'ticker' && tickerActive}
					<span class="mode-badge live">LIVE</span>
				{:else if m.id === 'textfx' && textAnimActive}
					<span class="mode-badge live">LIVE</span>
				{/if}
			</button>
		{/each}
	</div>

	<!-- Two-column body: controls left, preview right (template mode only) -->
	<div class="detail-body">
		<div class="controls-col">
			<!-- ═══════════════ TEMPLATE MODE ═══════════════ -->
			{#if sourceMode === 'template'}
				<div class="tpl-strip">
					{#each templateList as t}
						<button
							class="tpl-card"
							class:selected={templateId === t.id}
							onclick={() => onTemplateChange(t.id)}
							title={t.name}
							aria-label={t.name}
							aria-pressed={templateId === t.id}
							style="--tpl-accent: {tplAccent[t.id] ?? '#888'}"
						>
							<span class="tpl-accent-bar"></span>
							<span class="tpl-card-name">{tplShortName[t.id] ?? t.name}</span>
						</button>
					{/each}
				</div>

				{#if tpl}
					<div class="fields-section" class:grid-2={fieldLayout === 'grid-2'} class:grid-3={fieldLayout === 'grid-3'}>
						{#each tpl.fields as field}
							<label class="field-item">
								<span class="field-lbl">{field.label}</span>
								<input
									type="text"
									class="field-inp"
									value={fields[field.key] ?? ''}
									maxlength={field.maxLength}
									oninput={(e) => onFieldChange(field.key, (e.target as HTMLInputElement).value)}
								/>
							</label>
						{/each}
					</div>
				{/if}

				<div class="action-grid">
					<button class="act-btn on" onclick={onCutOn} disabled={layer.active || busy}>CUT ON</button>
					<button class="act-btn auto-on" onclick={onAutoOn} disabled={layer.active || busy}>AUTO ON</button>
					<button class="act-btn off" onclick={onCutOff} disabled={!layer.active || busy}>CUT OFF</button>
					<button class="act-btn auto-off" onclick={onAutoOff} disabled={!layer.active || busy}>AUTO OFF</button>
				</div>

			<!-- ═══════════════ IMAGE MODE ═══════════════ -->
			{:else if sourceMode === 'image'}
				<input
					bind:this={fileInput}
					type="file"
					accept="image/png"
					class="file-input-hidden"
					onchange={handleFileSelect}
				/>
				<div class="image-section">
					<button class="act-btn upload" onclick={() => fileInput?.click()}>UPLOAD PNG</button>
					{#if layer.imageName}
						<span class="image-info">{layer.imageName} ({layer.imageWidth}×{layer.imageHeight})</span>
						<button class="act-btn del" onclick={onImageDelete}>DELETE</button>
					{:else}
						<span class="ctl-unit">No image uploaded</span>
					{/if}
				</div>

				{#if layer.imageName}
					<div class="rect-controls">
						<label class="ctl-param" title="X position">
							<span>X</span>
							<input type="number" min="0" step="10"
								value={layer.x}
								onchange={(e) => onRectChange({ x: parseInt((e.target as HTMLInputElement).value) || 0, y: layer.y, width: layer.width, height: layer.height })}
							/>
						</label>
						<label class="ctl-param" title="Y position">
							<span>Y</span>
							<input type="number" min="0" step="10"
								value={layer.y}
								onchange={(e) => onRectChange({ x: layer.x, y: parseInt((e.target as HTMLInputElement).value) || 0, width: layer.width, height: layer.height })}
							/>
						</label>
						<label class="ctl-param" title="Width">
							<span>W</span>
							<input type="number" min="1" step="10"
								value={layer.width}
								onchange={(e) => onRectChange({ x: layer.x, y: layer.y, width: parseInt((e.target as HTMLInputElement).value) || 0, height: layer.height })}
							/>
						</label>
						<label class="ctl-param" title="Height">
							<span>H</span>
							<input type="number" min="1" step="10"
								value={layer.height}
								onchange={(e) => onRectChange({ x: layer.x, y: layer.y, width: layer.width, height: parseInt((e.target as HTMLInputElement).value) || 0 })}
							/>
						</label>
					</div>

					<div class="action-grid">
						<button class="act-btn on" onclick={onCutOn} disabled={layer.active || busy}>CUT ON</button>
						<button class="act-btn auto-on" onclick={onAutoOn} disabled={layer.active || busy}>AUTO ON</button>
						<button class="act-btn off" onclick={onCutOff} disabled={!layer.active || busy}>CUT OFF</button>
						<button class="act-btn auto-off" onclick={onAutoOff} disabled={!layer.active || busy}>AUTO OFF</button>
					</div>
				{/if}

			<!-- ═══════════════ TICKER MODE ═══════════════ -->
			{:else if sourceMode === 'ticker'}
				<div class="ticker-section">
					<input
						class="field-inp ticker-text"
						type="text"
						placeholder="Ticker text..."
						bind:value={tickerText}
						aria-label="Ticker text"
					/>
					<div class="ticker-params">
						<label class="ctl-param" title="Speed (px/s)">
							<span>spd</span>
							<input type="number" min="20" max="500" step="10" bind:value={tickerSpeed} />
						</label>
						<label class="ctl-param" title="Font size">
							<span>sz</span>
							<input type="number" min="16" max="72" step="2" bind:value={tickerFontSize} />
						</label>
						<label class="ctl-check" title="Bold">
							<input type="checkbox" bind:checked={tickerBold} />
							<span>B</span>
						</label>
						<label class="ctl-check" title="Loop">
							<input type="checkbox" bind:checked={tickerLoop} />
							<span>Loop</span>
						</label>
					</div>
					<div class="action-grid">
						{#if tickerActive}
							<button class="act-btn anim-stop" onclick={onTickerStop}>STOP</button>
							<button class="act-btn fly" onclick={() => onTickerUpdateText(tickerText)}>UPDATE</button>
						{:else}
							<button class="act-btn anim-start" onclick={() => onTickerStart({ text: tickerText, fontSize: tickerFontSize, speed: tickerSpeed, bold: tickerBold, loop: tickerLoop })}>START</button>
						{/if}
					</div>
				</div>

			<!-- ═══════════════ TEXT FX MODE ═══════════════ -->
			{:else}
				<div class="textfx-section">
					<select
						class="ctl-select"
						bind:value={textAnimMode}
						aria-label="Text animation mode"
					>
						<option value="typewriter">Typewriter</option>
						<option value="fade-word">Fade Word</option>
					</select>
					<input
						class="field-inp"
						type="text"
						placeholder="Text to animate..."
						bind:value={textAnimText}
						aria-label="Animation text"
					/>
					<div class="textfx-params">
						<label class="ctl-param" title="Font size">
							<span>sz</span>
							<input type="number" min="16" max="72" step="2" bind:value={textAnimFontSize} />
						</label>
						<label class="ctl-check" title="Bold">
							<input type="checkbox" bind:checked={textAnimBold} />
							<span>B</span>
						</label>
						{#if textAnimMode === 'typewriter'}
							<label class="ctl-param" title="Chars/sec">
								<span>cps</span>
								<input type="number" min="1" max="60" step="1" bind:value={textAnimCharsPerSec} />
							</label>
						{:else}
							<label class="ctl-param" title="Word delay (ms)">
								<span>delay</span>
								<input type="number" min="50" max="2000" step="50" bind:value={textAnimWordDelayMs} />
							</label>
							<label class="ctl-param" title="Fade duration (ms)">
								<span>fade</span>
								<input type="number" min="50" max="1000" step="50" bind:value={textAnimFadeDurationMs} />
							</label>
						{/if}
					</div>
					<div class="action-grid">
						{#if textAnimActive}
							<button class="act-btn anim-stop" onclick={onTextAnimStop}>STOP</button>
						{:else}
							<button class="act-btn anim-start" onclick={() => onTextAnimStart({
								mode: textAnimMode,
								text: textAnimText,
								fontSize: textAnimFontSize,
								bold: textAnimBold,
								charsPerSec: textAnimCharsPerSec,
								wordDelayMs: textAnimWordDelayMs,
								fadeDurationMs: textAnimFadeDurationMs,
							})}>START</button>
						{/if}
					</div>
				</div>
			{/if}

			<!-- ═══════════════ EFFECTS (any mode, when active) ═══════════════ -->
			{#if layer.active}
				<div class="effects-section">
					<div class="effects-row">
						<button class="disclosure" onclick={() => { flyOpen = !flyOpen; if (flyOpen) animOpen = false; }} aria-expanded={flyOpen} class:active-disc={flyOpen}>
							FLY
						</button>
						<button class="disclosure" onclick={() => { animOpen = !animOpen; if (animOpen) flyOpen = false; }} aria-expanded={animOpen} class:active-disc={animOpen}>
							ANIM
							{#if layer.animationMode}
								<span class="disclosure-badge">{layer.animationMode}</span>
							{/if}
						</button>
					</div>

					{#if flyOpen}
						<div class="collapse-content">
							<select
								class="ctl-select"
								value={flyConfig.direction}
								onchange={(e) => onFlyConfigChange('direction', (e.target as HTMLSelectElement).value)}
								aria-label="Fly direction"
							>
								<option value="left">Left</option>
								<option value="right">Right</option>
								<option value="top">Top</option>
								<option value="bottom">Bottom</option>
							</select>
							<input
								class="ctl-num"
								type="number"
								min="100"
								max="3000"
								step="100"
								value={flyConfig.durationMs}
								oninput={(e) => onFlyConfigChange('durationMs', parseInt((e.target as HTMLInputElement).value) || 500)}
								aria-label="Fly duration"
							/>
							<span class="ctl-unit">ms</span>
							<button class="act-btn fly" onclick={onFlyIn} disabled={busy}>FLY IN</button>
							<button class="act-btn fly" onclick={onFlyOut} disabled={!layer.active || busy}>FLY OUT</button>
						</div>
					{/if}

					{#if animOpen}
						<div class="collapse-content">
							<select
								class="ctl-select"
								value={animConfig.mode}
								onchange={(e) => onAnimConfigChange('mode', (e.target as HTMLSelectElement).value)}
								aria-label="Animation mode"
							>
								<option value="pulse">Pulse</option>
								<option value="transition">Transition</option>
							</select>
							{#if animConfig.mode === 'pulse'}
								<label class="ctl-param" title="Min opacity">
									<span>min</span>
									<input type="number" min="0" max="1" step="0.1"
										value={animConfig.minAlpha}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('minAlpha', Number.isNaN(v) ? 0 : v); }}
									/>
								</label>
								<label class="ctl-param" title="Max opacity">
									<span>max</span>
									<input type="number" min="0" max="1" step="0.1"
										value={animConfig.maxAlpha}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('maxAlpha', Number.isNaN(v) ? 1 : v); }}
									/>
								</label>
								<label class="ctl-param" title="Speed (Hz)">
									<span>Hz</span>
									<input type="number" min="0.1" max="5" step="0.1"
										value={animConfig.speedHz}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('speedHz', Number.isNaN(v) ? 1 : v); }}
									/>
								</label>
							{:else}
								<label class="ctl-param" title="Target opacity">
									<span>alpha</span>
									<input type="number" min="0" max="1" step="0.1"
										value={animConfig.toAlpha}
										oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onAnimConfigChange('toAlpha', Number.isNaN(v) ? 0.5 : v); }}
									/>
								</label>
								<label class="ctl-param" title="Duration (ms)">
									<span>ms</span>
									<input type="number" min="100" max="5000" step="100"
										value={animConfig.durationMs}
										oninput={(e) => onAnimConfigChange('durationMs', parseInt((e.target as HTMLInputElement).value) || 500)}
									/>
								</label>
							{/if}
							{#if layer.animationMode}
								<button class="act-btn anim-stop" onclick={onAnimateStop}>STOP</button>
							{:else}
								<button class="act-btn anim-start" onclick={onAnimate} disabled={!layer.active || busy}>ANIMATE</button>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		</div>

		<!-- Preview column: only in template mode -->
		{#if sourceMode === 'template'}
			<div class="preview-col">
				<canvas
					bind:this={previewCanvas}
					class="detail-preview"
					width={384}
					height={216}
					aria-label="Layer {layer.id} preview"
				></canvas>
			</div>
		{/if}
	</div>
</div>

<style>
	.detail-pane {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 6px 8px;
		overflow: hidden;
	}

	/* ── Mode selector bar ── */
	.mode-bar {
		display: flex;
		align-items: center;
		gap: 3px;
		flex-shrink: 0;
	}

	.mode-bar-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.1em;
		color: var(--text-tertiary);
		flex-shrink: 0;
		margin-right: 3px;
	}

	.mode-btn {
		display: flex;
		align-items: center;
		gap: 3px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 4px 10px;
		cursor: pointer;
		transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
	}

	.mode-btn:hover {
		color: var(--text-primary);
		border-color: var(--border-prominent, var(--border-default));
	}

	.mode-btn.selected {
		color: var(--text-primary);
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.mode-badge {
		font-family: var(--font-mono);
		font-size: 0.45rem;
		font-weight: 600;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 0 4px;
		border-radius: 2px;
	}

	.mode-badge.live {
		color: #fff;
		background: var(--tally-program);
		animation: badge-pulse 1.2s ease-in-out infinite;
	}

	@keyframes badge-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.5; }
	}

	/* ── Two-column body ── */
	.detail-body {
		display: flex;
		flex: 1;
		gap: 8px;
		min-height: 0;
		overflow: hidden;
	}

	.controls-col {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 4px;
		overflow-y: auto;
	}

	.preview-col {
		flex-shrink: 0;
		width: 220px;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 4px;
	}

	/* ── Preview ── */
	.detail-preview {
		width: 100%;
		aspect-ratio: 16 / 9;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: #0a0a0a;
		object-fit: contain;
	}

	/* ── Template strip ── */
	.tpl-strip {
		display: flex;
		gap: 3px;
		overflow-x: auto;
		flex-shrink: 0;
	}

	.tpl-card {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		min-width: 52px;
		height: 26px;
		padding: 0 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-base);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 0.5rem;
		font-weight: 500;
		letter-spacing: 0.02em;
		overflow: hidden;
		transition: border-color var(--transition-fast), background var(--transition-fast), color var(--transition-fast);
	}

	.tpl-card:hover {
		background: var(--bg-elevated);
		color: var(--text-primary);
	}

	.tpl-card.selected {
		border-color: var(--tpl-accent);
		color: var(--text-primary);
		background: var(--bg-elevated);
	}

	.tpl-accent-bar {
		position: absolute;
		top: 0;
		left: 0;
		right: 0;
		height: 2px;
		background: var(--tpl-accent);
		opacity: 0.4;
		transition: opacity var(--transition-fast);
	}

	.tpl-card.selected .tpl-accent-bar {
		opacity: 1;
	}

	.tpl-card-name {
		white-space: nowrap;
	}

	/* ── Field editors ── */
	.fields-section {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.fields-section.grid-2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 3px;
	}

	.fields-section.grid-3 {
		display: grid;
		grid-template-columns: 1fr 1fr 1fr;
		gap: 3px;
	}

	.field-item {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.field-lbl {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-tertiary);
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.field-inp {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 3px 6px;
		width: 100%;
	}

	.field-inp:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	/* ── Action buttons ── */
	.action-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(60px, 1fr));
		gap: 3px;
	}

	.act-btn {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.03em;
		padding: 5px 4px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast);
		white-space: nowrap;
	}

	.act-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.act-btn.on:not(:disabled):hover,
	.act-btn.auto-on:not(:disabled):hover {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
	}

	.act-btn.off:not(:disabled):hover,
	.act-btn.auto-off:not(:disabled):hover {
		border-color: var(--tally-preview);
		background: var(--tally-preview-dim);
	}

	.act-btn.fly:not(:disabled):hover,
	.act-btn.upload:not(:disabled):hover {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.act-btn.del:not(:disabled):hover {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
	}

	.act-btn.anim-start:not(:disabled):hover {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.act-btn.anim-stop {
		border-color: var(--tally-program);
		background: var(--tally-program-dim);
		color: #fff;
	}

	.act-btn.anim-stop:hover {
		background: var(--tally-program-light);
	}

	/* ── Image section ── */
	.image-section {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
	}

	.image-info {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-secondary);
	}

	.file-input-hidden {
		display: none;
	}

	/* ── Rect controls ── */
	.rect-controls {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
	}

	/* ── Ticker section ── */
	.ticker-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.ticker-text {
		width: 100%;
	}

	.ticker-params {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
	}

	/* ── Text FX section ── */
	.textfx-section {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.textfx-params {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-wrap: wrap;
	}

	/* ── Effects section ── */
	.effects-section {
		border-top: 1px solid var(--border-subtle);
		padding-top: 4px;
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.effects-row {
		display: flex;
		gap: 3px;
	}

	.disclosure {
		display: flex;
		align-items: center;
		gap: 3px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		text-transform: uppercase;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		cursor: pointer;
		padding: 3px 8px;
		transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
	}

	.disclosure:hover {
		color: var(--text-primary);
		border-color: var(--border-prominent, var(--border-default));
	}

	.disclosure.active-disc {
		color: var(--text-primary);
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
	}

	.disclosure-badge {
		font-family: var(--font-mono);
		font-size: 0.45rem;
		font-weight: 600;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 0 4px;
		border-radius: 2px;
	}

	/* ── Collapse content ── */
	.collapse-content {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: wrap;
		padding: 2px 0;
	}

	/* ── Shared controls ── */
	.ctl-select {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
	}

	.ctl-num {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 4px;
		width: 48px;
	}

	.ctl-unit {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
	}

	.ctl-param {
		display: flex;
		align-items: center;
		gap: 2px;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-secondary);
	}

	.ctl-param input {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 1px 3px;
		width: 38px;
	}

	.ctl-check {
		display: flex;
		align-items: center;
		gap: 2px;
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-secondary);
		cursor: pointer;
	}

	.ctl-check input[type="checkbox"] {
		width: 12px;
		height: 12px;
		accent-color: var(--accent-blue);
	}
</style>

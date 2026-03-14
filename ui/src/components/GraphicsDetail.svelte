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

	interface Props {
		layer: GraphicsLayerState;
		templateId: string;
		fields: Record<string, string>;
		animConfig: AnimConfig;
		flyConfig: FlyConfig;
		publisher: GraphicsPublisher;
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
	}

	let {
		layer, templateId, fields, animConfig, flyConfig, publisher,
		onTemplateChange, onFieldChange, onAnimConfigChange, onFlyConfigChange,
		onCutOn, onAutoOn, onCutOff, onAutoOff,
		onFlyIn, onFlyOut, onAnimate, onAnimateStop
	}: Props = $props();

	let previewCanvas = $state<HTMLCanvasElement | null>(null);
	let flyOpen = $state(false);
	let animOpen = $state(false);

	const tpl = $derived(builtinTemplates[templateId]);
	const busy = $derived(
		!!layer.animationMode ||
		(layer.active && layer.fadePosition != null && layer.fadePosition > 0 && layer.fadePosition < 1)
	);

	// Template accent colors for visual distinction in the gallery strip
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

	// Re-render preview when fields or template change
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

	// Determine if fields should use 2-column or 3-column layout
	const fieldLayout = $derived.by(() => {
		if (!tpl) return 'single';
		if (tpl.fields.length >= 4) return 'grid-3';
		if (tpl.fields.length === 2) return 'grid-2';
		return 'single';
	});
</script>

<div class="detail-pane">
	<!-- Template gallery strip -->
	<div class="tpl-strip">
		<span class="tpl-strip-label">TEMPLATE</span>
		<div class="tpl-cards">
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
	</div>

	<!-- Canvas preview -->
	<canvas
		bind:this={previewCanvas}
		class="detail-preview"
		width={384}
		height={216}
		aria-label="Layer {layer.id} preview"
	></canvas>

	<!-- Field editors -->
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

	<!-- Action buttons -->
	<div class="action-grid">
		<button class="act-btn on" onclick={onCutOn} disabled={layer.active || busy}>CUT ON</button>
		<button class="act-btn auto-on" onclick={onAutoOn} disabled={layer.active || busy}>AUTO ON</button>
		<button class="act-btn off" onclick={onCutOff} disabled={!layer.active || busy}>CUT OFF</button>
		<button class="act-btn auto-off" onclick={onAutoOff} disabled={!layer.active || busy}>AUTO OFF</button>
	</div>

	<!-- Collapsible: Fly controls -->
	<button class="disclosure" onclick={() => flyOpen = !flyOpen} aria-expanded={flyOpen}>
		<span class="disclosure-arrow" class:open={flyOpen}>&#9654;</span>
		FLY
	</button>
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
			<button class="act-btn fly" onclick={onFlyIn} disabled={layer.active || busy}>FLY IN</button>
			<button class="act-btn fly" onclick={onFlyOut} disabled={!layer.active || busy}>FLY OUT</button>
		</div>
	{/if}

	<!-- Collapsible: Animation controls -->
	<button class="disclosure" onclick={() => animOpen = !animOpen} aria-expanded={animOpen}>
		<span class="disclosure-arrow" class:open={animOpen}>&#9654;</span>
		ANIM
		{#if layer.animationMode}
			<span class="disclosure-badge">{layer.animationMode}</span>
		{/if}
	</button>
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

<style>
	.detail-pane {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		gap: 5px;
		padding: 6px 8px;
		overflow-y: auto;
	}

	/* ── Template strip ── */
	.tpl-strip {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.tpl-strip-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.1em;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.tpl-cards {
		display: flex;
		gap: 3px;
		overflow-x: auto;
		flex: 1;
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

	/* ── Preview ── */
	.detail-preview {
		width: 100%;
		aspect-ratio: 16 / 9;
		max-height: 90px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: #0a0a0a;
		object-fit: contain;
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
		grid-template-columns: 1fr 1fr 1fr 1fr;
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

	.act-btn.fly:not(:disabled):hover {
		border-color: var(--accent-blue);
		background: var(--accent-blue-dim);
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

	/* ── Disclosure toggles ── */
	.disclosure {
		display: flex;
		align-items: center;
		gap: 4px;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
		text-transform: uppercase;
		background: none;
		border: none;
		cursor: pointer;
		padding: 2px 0;
	}

	.disclosure:hover {
		color: var(--text-primary);
	}

	.disclosure-arrow {
		font-size: 0.45rem;
		transition: transform var(--transition-fast);
		display: inline-block;
	}

	.disclosure-arrow.open {
		transform: rotate(90deg);
	}

	.disclosure-badge {
		font-family: var(--font-mono);
		font-size: 0.45rem;
		font-weight: 600;
		color: var(--accent-blue);
		background: var(--accent-blue-dim);
		padding: 0 4px;
		border-radius: 2px;
		margin-left: 2px;
	}

	.collapse-content {
		display: flex;
		align-items: center;
		gap: 4px;
		flex-wrap: wrap;
		padding-left: 12px;
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
</style>

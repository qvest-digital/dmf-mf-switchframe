<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { graphicsOn, graphicsOff, graphicsAutoOn, graphicsAutoOff, fireAndForget } from '$lib/api/switch-api';
	import { GraphicsPublisher } from '$lib/graphics/publisher';
	import { templateList, builtinTemplates, type GraphicsTemplate } from '$lib/graphics/templates';

	interface Props {
		state: ControlRoomState;
		onTemplateChange?: (template: GraphicsTemplate | null, values: Record<string, string>) => void;
	}
	let { state: crState, onTemplateChange }: Props = $props();

	let selectedTemplateId = $state('lower-third');
	let fieldValues = $state<Record<string, string>>(getDefaultValues('lower-third'));
	let previewCanvas: HTMLCanvasElement | null = $state(null);

	const publisher = new GraphicsPublisher();
	publisher.init(320, 240);

	const selectedTemplate = $derived(builtinTemplates[selectedTemplateId]);
	const graphicsActive = $derived(crState.graphics?.active ?? false);

	function getDefaultValues(templateId: string): Record<string, string> {
		const tpl = builtinTemplates[templateId];
		if (!tpl) return {};
		const values: Record<string, string> = {};
		for (const field of tpl.fields) {
			values[field.key] = field.defaultValue;
		}
		return values;
	}

	// Re-render preview when fields change, and notify parent
	$effect(() => {
		const tpl = selectedTemplate;
		const vals = fieldValues;
		const canvas = previewCanvas;
		if (!tpl || !canvas) return;
		try {
			publisher.renderPreview(canvas, tpl, vals);
		} catch {
			// Canvas rendering may fail in test environments
		}
		onTemplateChange?.(tpl, vals);
	});

	async function handlePublishAndOn() {
		const tpl = selectedTemplate;
		if (!tpl) return;
		try {
			await publisher.publish(tpl, fieldValues);
			fireAndForget(graphicsOn());
		} catch (err) {
			console.warn('Graphics publish failed:', err);
		}
	}

	async function handlePublishAndAutoOn() {
		const tpl = selectedTemplate;
		if (!tpl) return;
		try {
			await publisher.publish(tpl, fieldValues);
			fireAndForget(graphicsAutoOn());
		} catch (err) {
			console.warn('Graphics publish failed:', err);
		}
	}

	function handleOff() {
		fireAndForget(graphicsOff());
	}

	function handleAutoOff() {
		fireAndForget(graphicsAutoOff());
	}

	function handleTemplateChange(e: Event) {
		const id = (e.target as HTMLSelectElement).value;
		selectedTemplateId = id;
		fieldValues = getDefaultValues(id);
	}
</script>

<div class="graphics-panel">
	<div class="gfx-header">
		<span class="gfx-label">DSK</span>
		<span class="gfx-status" class:on-air={graphicsActive}>
			{graphicsActive ? 'ON AIR' : 'OFF'}
		</span>
	</div>

	<div class="gfx-controls">
		<select class="template-select" value={selectedTemplateId} onchange={handleTemplateChange} aria-label="Graphics template">
			{#each templateList as tpl}
				<option value={tpl.id}>{tpl.name}</option>
			{/each}
		</select>

		{#if selectedTemplate}
			<div class="fields">
				{#each selectedTemplate.fields as field}
					<label class="field-row">
						<span class="field-label">{field.label}</span>
						<input
							type="text"
							class="field-input"
							value={fieldValues[field.key] ?? ''}
							maxlength={field.maxLength}
							oninput={(e) => { fieldValues = { ...fieldValues, [field.key]: (e.target as HTMLInputElement).value }; }}
						/>
					</label>
				{/each}
			</div>
		{/if}

		<canvas
			bind:this={previewCanvas}
			class="gfx-preview"
			width={320}
			height={240}
			aria-label="Graphics preview"
		></canvas>

		<div class="gfx-buttons">
			<button class="gfx-btn on" onclick={handlePublishAndOn} disabled={graphicsActive}>
				CUT ON
			</button>
			<button class="gfx-btn auto-on" onclick={handlePublishAndAutoOn} disabled={graphicsActive}>
				AUTO ON
			</button>
			<button class="gfx-btn off" onclick={handleOff} disabled={!graphicsActive}>
				CUT OFF
			</button>
			<button class="gfx-btn auto-off" onclick={handleAutoOff} disabled={!graphicsActive}>
				AUTO OFF
			</button>
		</div>
	</div>
</div>

<style>
	.graphics-panel {
		display: flex;
		flex-direction: column;
		gap: 6px;
		padding: 8px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		background: var(--bg-surface);
	}

	.gfx-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding-bottom: 4px;
		border-bottom: 1px solid var(--border-subtle);
	}

	.gfx-label {
		font-family: var(--font-ui);
		font-size: 0.75rem;
		font-weight: 700;
		letter-spacing: 0.06em;
		color: var(--text-primary);
	}

	.gfx-status {
		font-family: var(--font-mono);
		font-size: 0.65rem;
		font-weight: 600;
		color: var(--text-secondary);
		padding: 1px 6px;
		border-radius: var(--radius-sm);
		background: var(--bg-base);
	}

	.gfx-status.on-air {
		color: #fff;
		background: var(--tally-program, #dc2626);
		animation: pulse-glow 1.5s ease-in-out infinite;
	}

	@keyframes pulse-glow {
		0%, 100% { box-shadow: 0 0 4px rgba(220, 38, 38, 0.3); }
		50% { box-shadow: 0 0 8px rgba(220, 38, 38, 0.6); }
	}

	.gfx-controls {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.template-select {
		font-family: var(--font-ui);
		font-size: 0.7rem;
		background: var(--bg-elevated);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 3px 6px;
		cursor: pointer;
	}

	.fields {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.field-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.field-label {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		color: var(--text-secondary);
		min-width: 40px;
		flex-shrink: 0;
	}

	.field-input {
		flex: 1;
		font-family: var(--font-ui);
		font-size: 0.7rem;
		background: var(--bg-base);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 2px 6px;
	}

	.field-input:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.gfx-preview {
		width: 100%;
		aspect-ratio: 4 / 3;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: #111;
	}

	.gfx-buttons {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 3px;
	}

	.gfx-btn {
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 600;
		letter-spacing: 0.04em;
		padding: 4px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-primary);
		cursor: pointer;
		transition: border-color var(--transition-fast), background var(--transition-fast);
	}

	.gfx-btn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.gfx-btn.on:not(:disabled):hover,
	.gfx-btn.auto-on:not(:disabled):hover {
		border-color: var(--tally-program, #dc2626);
		background: rgba(220, 38, 38, 0.15);
	}

	.gfx-btn.off:not(:disabled):hover,
	.gfx-btn.auto-off:not(:disabled):hover {
		border-color: var(--tally-preview, #16a34a);
		background: rgba(22, 163, 74, 0.15);
	}
</style>

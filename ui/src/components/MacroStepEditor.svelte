<script lang="ts">
	import type { MacroAction, MacroStep } from '$lib/api/types';
	import { ACTION_META, CATEGORIES, SOURCE_ACTIONS, WIPE_DIRECTIONS, GRAPHICS_LAYER_ACTIONS, FLY_DIRECTIONS } from './macro-actions';
	import { templateList } from '$lib/graphics/templates';

	interface Props {
		step: MacroStep;
		index: number;
		sourceKeys: string[];
		sourceLabel: (key: string) => string;
		stingerNames: string[];
		presetNames: { id: string; name: string }[];
		onupdate: (key: string, value: unknown) => void;
		onchangeaction: (action: MacroAction) => void;
	}
	let { step, index, sourceKeys, sourceLabel, stingerNames, presetNames, onupdate, onchangeaction }: Props = $props();

	let transType = $derived((step.params.type as string) || 'mix');
	let needsSource = $derived(SOURCE_ACTIONS.includes(step.action));
	let needsLayerId = $derived(GRAPHICS_LAYER_ACTIONS.includes(step.action));
	let animMode = $derived((step.params.mode as string) || 'pulse');

	// Validation warnings
	let warnings = $derived.by(() => {
		const w: string[] = [];
		if (step.action === 'transition') {
			if (transType === 'wipe' && !step.params.wipeDirection) {
				w.push('Wipe direction is required');
			}
			if (transType === 'stinger' && !step.params.stingerName) {
				w.push('Stinger name is required');
			}
		}
		return w;
	});
</script>

<div class="step-editor" data-testid="step-editor-{index}">
	<!-- Action select -->
	<div class="field-row">
		<span class="field-label">Action</span>
		<select
			class="field-select action-select"
			value={step.action}
			onchange={(e) => onchangeaction((e.target as HTMLSelectElement).value as MacroAction)}
		>
			{#each CATEGORIES as category}
				<optgroup label={category}>
					{#each Object.entries(ACTION_META).filter(([, m]) => m.category === category) as [action, meta]}
						<option value={action} title={meta.description}>{meta.label}</option>
					{/each}
				</optgroup>
			{/each}
		</select>
	</div>

	<!-- Source picker -->
	{#if needsSource}
		<div class="field-row">
			<span class="field-label">Source</span>
			<select
				class="field-select source-select"
				value={step.params.source as string || ''}
				onchange={(e) => onupdate('source', (e.target as HTMLSelectElement).value)}
			>
				{#each sourceKeys as key}
					<option value={key}>{sourceLabel(key)}</option>
				{/each}
			</select>
		</div>
	{/if}

	<!-- Graphics: Layer ID -->
	{#if needsLayerId}
		<div class="field-row">
			<span class="field-label">Layer</span>
			<input
				class="field-input"
				type="number"
				min="0"
				step="1"
				value={step.params.layerId as number ?? 0}
				oninput={(e) => onupdate('layerId', parseInt((e.target as HTMLInputElement).value) || 0)}
			/>
		</div>
	{/if}

	<!-- Graphics: Fly In / Fly Out direction + duration -->
	{#if step.action === 'graphics_fly_in' || step.action === 'graphics_fly_out'}
		<div class="field-row">
			<span class="field-label">Direction</span>
			<select
				class="field-select"
				value={step.params.direction as string || 'left'}
				onchange={(e) => onupdate('direction', (e.target as HTMLSelectElement).value)}
			>
				{#each FLY_DIRECTIONS as dir}
					<option value={dir.value}>{dir.label}</option>
				{/each}
			</select>
		</div>
		<div class="field-row">
			<span class="field-label">Duration</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="100"
					max="5000"
					step="100"
					value={step.params.durationMs as number || 500}
					oninput={(e) => onupdate('durationMs', parseInt((e.target as HTMLInputElement).value) || 500)}
				/>
				<span class="field-unit">ms</span>
			</div>
		</div>
	{/if}

	<!-- Graphics: Set Rect / Slide -->
	{#if step.action === 'graphics_set_rect' || step.action === 'graphics_slide'}
		<div class="field-row">
			<span class="field-label">X</span>
			<input class="field-input" type="number" min="0" step="1"
				value={step.params.x as number ?? 0}
				oninput={(e) => onupdate('x', parseInt((e.target as HTMLInputElement).value) || 0)} />
			<span class="field-label">Y</span>
			<input class="field-input" type="number" min="0" step="1"
				value={step.params.y as number ?? 0}
				oninput={(e) => onupdate('y', parseInt((e.target as HTMLInputElement).value) || 0)} />
		</div>
		<div class="field-row">
			<span class="field-label">W</span>
			<input class="field-input" type="number" min="1" step="1"
				value={step.params.width as number ?? 1920}
				oninput={(e) => onupdate('width', parseInt((e.target as HTMLInputElement).value) || 1920)} />
			<span class="field-label">H</span>
			<input class="field-input" type="number" min="1" step="1"
				value={step.params.height as number ?? 1080}
				oninput={(e) => onupdate('height', parseInt((e.target as HTMLInputElement).value) || 1080)} />
		</div>
		{#if step.action === 'graphics_slide'}
			<div class="field-row">
				<span class="field-label">Duration</span>
				<div class="field-with-unit">
					<input class="field-input" type="number" min="100" max="5000" step="100"
						value={step.params.durationMs as number || 500}
						oninput={(e) => onupdate('durationMs', parseInt((e.target as HTMLInputElement).value) || 500)} />
					<span class="field-unit">ms</span>
				</div>
			</div>
		{/if}
	{/if}

	<!-- Graphics: Set Z-Order -->
	{#if step.action === 'graphics_set_zorder'}
		<div class="field-row">
			<span class="field-label">Z-Order</span>
			<input class="field-input" type="number" min="0" step="1"
				value={step.params.zOrder as number ?? 0}
				oninput={(e) => onupdate('zOrder', parseInt((e.target as HTMLInputElement).value) || 0)} />
		</div>
	{/if}

	<!-- Graphics: Animate -->
	{#if step.action === 'graphics_animate'}
		<div class="field-row">
			<span class="field-label">Mode</span>
			<select class="field-select"
				value={animMode}
				onchange={(e) => onupdate('mode', (e.target as HTMLSelectElement).value)}
			>
				<option value="pulse">Pulse</option>
				<option value="transition">Transition</option>
			</select>
		</div>
		{#if animMode === 'pulse'}
			<div class="field-row">
				<span class="field-label">Min α</span>
				<input class="field-input" type="number" min="0" max="1" step="0.1"
					value={step.params.minAlpha as number ?? 0.3}
					oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onupdate('minAlpha', Number.isNaN(v) ? 0.3 : v); }} />
				<span class="field-label">Max α</span>
				<input class="field-input" type="number" min="0" max="1" step="0.1"
					value={step.params.maxAlpha as number ?? 1.0}
					oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onupdate('maxAlpha', Number.isNaN(v) ? 1.0 : v); }} />
			</div>
			<div class="field-row">
				<span class="field-label">Speed</span>
				<div class="field-with-unit">
					<input class="field-input" type="number" min="0.1" max="5" step="0.1"
						value={step.params.speedHz as number ?? 1.0}
						oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onupdate('speedHz', Number.isNaN(v) ? 1.0 : v); }} />
					<span class="field-unit">Hz</span>
				</div>
			</div>
		{:else}
			<div class="field-row">
				<span class="field-label">To α</span>
				<input class="field-input" type="number" min="0" max="1" step="0.1"
					value={step.params.toAlpha as number ?? 0.5}
					oninput={(e) => { const v = parseFloat((e.target as HTMLInputElement).value); onupdate('toAlpha', Number.isNaN(v) ? 0.5 : v); }} />
			</div>
			<div class="field-row">
				<span class="field-label">Duration</span>
				<div class="field-with-unit">
					<input class="field-input" type="number" min="100" max="5000" step="100"
						value={step.params.durationMs as number || 500}
						oninput={(e) => onupdate('durationMs', parseInt((e.target as HTMLInputElement).value) || 500)} />
					<span class="field-unit">ms</span>
				</div>
			</div>
		{/if}
	{/if}

	<!-- Graphics: Upload Frame (template select) -->
	{#if step.action === 'graphics_upload_frame'}
		<div class="field-row">
			<span class="field-label">Template</span>
			<select class="field-select"
				value={step.params.template as string || 'lower-third'}
				onchange={(e) => onupdate('template', (e.target as HTMLSelectElement).value)}
			>
				{#each templateList as t}
					<option value={t.id}>{t.name}</option>
				{/each}
			</select>
		</div>
	{/if}

	<!-- Transition fields -->
	{#if step.action === 'transition'}
		<div class="field-row">
			<span class="field-label">Type</span>
			<select
				class="field-select transition-type-select"
				value={transType}
				onchange={(e) => onupdate('type', (e.target as HTMLSelectElement).value)}
			>
				<option value="mix">Mix (Dissolve)</option>
				<option value="dip">Dip</option>
				<option value="wipe">Wipe</option>
				<option value="stinger">Stinger</option>
			</select>
		</div>

		<!-- Wipe direction -->
		{#if transType === 'wipe'}
			<div class="field-row">
				<span class="field-label">Direction</span>
				<select
					class="field-select wipe-direction-select"
					value={step.params.wipeDirection as string || ''}
					onchange={(e) => onupdate('wipeDirection', (e.target as HTMLSelectElement).value)}
				>
					<option value="" disabled>Select direction...</option>
					{#each WIPE_DIRECTIONS as dir}
						<option value={dir.value}>{dir.label}</option>
					{/each}
				</select>
			</div>
		{/if}

		<!-- Stinger picker -->
		{#if transType === 'stinger'}
			<div class="field-row">
				<span class="field-label">Stinger</span>
				<select
					class="field-select stinger-select"
					value={step.params.stingerName as string || ''}
					onchange={(e) => onupdate('stingerName', (e.target as HTMLSelectElement).value)}
				>
					<option value="" disabled>Select stinger...</option>
					{#each stingerNames as name}
						<option value={name}>{name}</option>
					{/each}
				</select>
			</div>
		{/if}

		<!-- Duration -->
		<div class="field-row">
			<span class="field-label">Duration</span>
			<div class="field-with-unit">
				<input
					class="field-input duration-input"
					type="number"
					min="100"
					max="5000"
					step="100"
					value={step.params.durationMs as number || 1000}
					oninput={(e) => onupdate('durationMs', parseInt((e.target as HTMLInputElement).value) || 1000)}
				/>
				<span class="field-unit">ms</span>
			</div>
		</div>
	{/if}

	<!-- Wait -->
	{#if step.action === 'wait'}
		<div class="field-row">
			<span class="field-label">Duration</span>
			<div class="field-with-unit">
				<input
					class="field-input wait-duration-input"
					type="number"
					min="0"
					max="30000"
					step="100"
					value={step.params.ms as number || 500}
					oninput={(e) => onupdate('ms', parseInt((e.target as HTMLInputElement).value) || 0)}
				/>
				<span class="field-unit">ms</span>
			</div>
		</div>
	{/if}

	<!-- set_audio level -->
	{#if step.action === 'set_audio'}
		<div class="field-row">
			<span class="field-label">Level</span>
			<div class="field-with-unit">
				<input
					class="field-input level-input"
					type="number"
					min="-60"
					max="20"
					step="1"
					value={step.params.level as number ?? 0}
					oninput={(e) => onupdate('level', parseFloat((e.target as HTMLInputElement).value) || 0)}
				/>
				<span class="field-unit">dB</span>
			</div>
		</div>
	{/if}

	<!-- audio_mute -->
	{#if step.action === 'audio_mute'}
		<div class="field-row">
			<span class="field-label">Muted</span>
			<input
				class="field-checkbox"
				type="checkbox"
				checked={step.params.muted as boolean ?? true}
				onchange={(e) => onupdate('muted', (e.target as HTMLInputElement).checked)}
			/>
		</div>
	{/if}

	<!-- audio_afv -->
	{#if step.action === 'audio_afv'}
		<div class="field-row">
			<span class="field-label">AFV</span>
			<input
				class="field-checkbox"
				type="checkbox"
				checked={step.params.afv as boolean ?? true}
				onchange={(e) => onupdate('afv', (e.target as HTMLInputElement).checked)}
			/>
		</div>
	{/if}

	<!-- audio_trim -->
	{#if step.action === 'audio_trim'}
		<div class="field-row">
			<span class="field-label">Trim</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="-20"
					max="20"
					step="0.5"
					value={step.params.trim as number ?? 0}
					oninput={(e) => onupdate('trim', parseFloat((e.target as HTMLInputElement).value) || 0)}
				/>
				<span class="field-unit">dB</span>
			</div>
		</div>
	{/if}

	<!-- audio_master -->
	{#if step.action === 'audio_master'}
		<div class="field-row">
			<span class="field-label">Level</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="-60"
					max="20"
					step="1"
					value={step.params.level as number ?? 0}
					oninput={(e) => onupdate('level', parseFloat((e.target as HTMLInputElement).value) || 0)}
				/>
				<span class="field-unit">dB</span>
			</div>
		</div>
	{/if}

	<!-- audio_delay -->
	{#if step.action === 'audio_delay'}
		<div class="field-row">
			<span class="field-label">Delay</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="0"
					max="500"
					step="1"
					value={step.params.delayMs as number ?? 0}
					oninput={(e) => onupdate('delayMs', parseInt((e.target as HTMLInputElement).value) || 0)}
				/>
				<span class="field-unit">ms</span>
			</div>
		</div>
	{/if}

	<!-- source_delay -->
	{#if step.action === 'source_delay'}
		<div class="field-row">
			<span class="field-label">Delay</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="0"
					max="500"
					step="1"
					value={step.params.delayMs as number ?? 0}
					oninput={(e) => onupdate('delayMs', parseInt((e.target as HTMLInputElement).value) || 0)}
				/>
				<span class="field-unit">ms</span>
			</div>
		</div>
	{/if}

	<!-- source_label -->
	{#if step.action === 'source_label'}
		<div class="field-row">
			<span class="field-label">Label</span>
			<input
				class="field-input"
				type="text"
				value={step.params.label as string || ''}
				oninput={(e) => onupdate('label', (e.target as HTMLInputElement).value)}
				placeholder="New label"
			/>
		</div>
	{/if}

	<!-- source_position -->
	{#if step.action === 'source_position'}
		<div class="field-row">
			<span class="field-label">Position</span>
			<input
				class="field-input"
				type="number"
				min="1"
				max="20"
				step="1"
				value={step.params.position as number ?? 1}
				oninput={(e) => onupdate('position', parseInt((e.target as HTMLInputElement).value) || 1)}
			/>
		</div>
	{/if}

	<!-- preset_recall -->
	{#if step.action === 'preset_recall'}
		<div class="field-row">
			<span class="field-label">Preset</span>
			<select
				class="field-select preset-select"
				value={step.params.id as string || ''}
				onchange={(e) => onupdate('id', (e.target as HTMLSelectElement).value)}
			>
				<option value="" disabled>Select preset...</option>
				{#each presetNames as p}
					<option value={p.id}>{p.name}</option>
				{/each}
			</select>
		</div>
	{/if}

	<!-- replay_play -->
	{#if step.action === 'replay_play'}
		<div class="field-row">
			<span class="field-label">Speed</span>
			<select
				class="field-select"
				value={String(step.params.speed ?? 0.5)}
				onchange={(e) => onupdate('speed', parseFloat((e.target as HTMLSelectElement).value))}
			>
				<option value="0.25">0.25x</option>
				<option value="0.5">0.5x</option>
				<option value="0.75">0.75x</option>
				<option value="1">1x</option>
			</select>
		</div>
		<div class="field-row">
			<span class="field-label">Loop</span>
			<input
				class="field-checkbox"
				type="checkbox"
				checked={step.params.loop as boolean ?? false}
				onchange={(e) => onupdate('loop', (e.target as HTMLInputElement).checked)}
			/>
		</div>
	{/if}

	<!-- replay_quick_clip -->
	{#if step.action === 'replay_quick_clip'}
		<div class="field-row">
			<span class="field-label">Duration</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="1"
					max="300"
					step="1"
					value={step.params.durationSecs as number ?? 10}
					oninput={(e) => onupdate('durationSecs', parseInt((e.target as HTMLInputElement).value) || 10)}
				/>
				<span class="field-unit">sec</span>
			</div>
		</div>
		<div class="field-row">
			<span class="field-label">Speed</span>
			<select
				class="field-select"
				value={String(step.params.speed ?? 0.5)}
				onchange={(e) => onupdate('speed', parseFloat((e.target as HTMLSelectElement).value))}
			>
				<option value="0.25">0.25x</option>
				<option value="0.5">0.5x</option>
				<option value="0.75">0.75x</option>
				<option value="1">1x</option>
			</select>
		</div>
	{/if}

	<!-- SCTE-35 Cue -->
	{#if step.action === 'scte35_cue'}
		<div class="field-row">
			<span class="field-label">Duration</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="1"
					max="3600"
					step="1"
					value={((step.params.durationMs as number) || 30000) / 1000}
					oninput={(e) => onupdate('durationMs', (parseInt((e.target as HTMLInputElement).value) || 30) * 1000)}
				/>
				<span class="field-unit">sec</span>
			</div>
		</div>
		<div class="field-row">
			<span class="field-label">Auto-return</span>
			<input
				class="field-checkbox"
				type="checkbox"
				checked={step.params.autoReturn as boolean ?? true}
				onchange={(e) => onupdate('autoReturn', (e.target as HTMLInputElement).checked)}
			/>
		</div>
	{/if}

	<!-- SCTE-35 Event ID (return, cancel, hold, extend) -->
	{#if ['scte35_return', 'scte35_cancel', 'scte35_hold', 'scte35_extend'].includes(step.action)}
		<div class="field-row">
			<span class="field-label">Event ID</span>
			<input
				class="field-input event-id-input"
				type="number"
				min="0"
				step="1"
				value={step.params.eventId as number || 0}
				oninput={(e) => onupdate('eventId', parseInt((e.target as HTMLInputElement).value) || 0)}
				placeholder="0 = most recent"
			/>
		</div>
	{/if}

	<!-- SCTE-35 Extend duration -->
	{#if step.action === 'scte35_extend'}
		<div class="field-row">
			<span class="field-label">Extend by</span>
			<div class="field-with-unit">
				<input
					class="field-input"
					type="number"
					min="1000"
					max="600000"
					step="1000"
					value={step.params.durationMs as number || 30000}
					oninput={(e) => onupdate('durationMs', parseInt((e.target as HTMLInputElement).value) || 30000)}
				/>
				<span class="field-unit">ms</span>
			</div>
		</div>
	{/if}

	<!-- Validation warnings -->
	{#if warnings.length > 0}
		<div class="step-warnings">
			{#each warnings as w}
				<div class="step-warning">{w}</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.step-editor {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.field-row {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.field-label {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		min-width: 52px;
		flex-shrink: 0;
	}

	.field-select, .field-input {
		flex: 1;
		padding: 3px 4px;
		background: var(--bg-base);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-primary);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		min-width: 0;
	}

	.field-select:focus, .field-input:focus {
		border-color: var(--accent-blue);
		outline: none;
	}

	.field-with-unit {
		flex: 1;
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.field-unit {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.field-checkbox {
		accent-color: var(--accent-blue);
	}

	.step-warnings {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.step-warning {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: #f59e0b;
		padding: 2px 4px;
		background: rgba(245, 158, 11, 0.1);
		border-radius: var(--radius-sm);
	}
</style>

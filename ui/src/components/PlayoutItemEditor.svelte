<script lang="ts">
	import type { PlaylistItem, PlayoutClip, SecondaryEvent, WipeConfig, GraphicsLayerState, SourceInfo } from '$lib/api/types';
	import { liveSourceOptions } from '$lib/util/sort-sources';
	import SecondaryEventEditor from './SecondaryEventEditor.svelte';
	import WipePatternBrowser from './WipePatternBrowser.svelte';

	interface Props {
		item: PlaylistItem;
		onChange: (updated: PlaylistItem) => void;
		sources?: Record<string, SourceInfo>;
		clips?: PlayoutClip[];
		graphicsLayers?: GraphicsLayerState[];
	}

	let { item, onChange, sources = {}, clips = [], graphicsLayers = [] }: Props = $props();

	let sourceOptions = $derived(liveSourceOptions(sources, item.source));

	const transitionTypes = ['cut', 'dissolve', 'dip_black', 'dip_white', 'wipe'] as const;

	const defaultWipeConfig: WipeConfig = {
		pattern: 'h-left',
		soft_edge: 0.005,
		border_width: 0,
		border_color_y: 235,
		border_color_cb: 128,
		border_color_cr: 128,
		reverse: false,
		offset_x: 0,
		offset_y: 0,
		multiply: 1,
	};

	let itemWipeConfig = $state<WipeConfig>({ ...defaultWipeConfig });

	function handleWipeConfigChange(wc: WipeConfig) {
		itemWipeConfig = wc;
		emit({
			transition: {
				type: currentTransitionType,
				duration_ms: currentTransitionDuration,
				wipe_config: wc,
			},
		});
	}

	let currentTransitionType = $derived(item.transition?.type ?? 'cut');
	let currentTransitionDuration = $derived(item.transition?.duration_ms ?? 500);

	function emit(patch: Partial<PlaylistItem>) {
		onChange({ ...item, ...patch });
	}

	function handleTypeChange(newType: PlaylistItem['type']) {
		// Reset type-specific fields when changing types
		const base: Partial<PlaylistItem> = {
			type: newType,
			asset_uri: undefined,
			source: undefined,
			in_point_ms: undefined,
			out_point_ms: undefined,
			loop: undefined,
			hold: undefined,
			duration_ms: undefined,
		};
		onChange({ ...item, ...base });
	}

	function handleTransitionTypeChange(type: string) {
		if (type === 'cut') {
			emit({ transition: undefined });
		} else {
			emit({ transition: { type, duration_ms: currentTransitionDuration } });
		}
	}

	function handleTransitionDurationChange(ms: number) {
		emit({ transition: { type: currentTransitionType, duration_ms: ms } });
	}

	function handleSecondaryEventsUpdate(events: SecondaryEvent[]) {
		emit({ secondary_events: events.length > 0 ? events : undefined });
	}
</script>

<div class="item-editor">
	<!-- Header row: type + title -->
	<div class="editor-section">
		<div class="editor-row">
			<label class="editor-field">
				<span class="editor-label">Type</span>
				<select
					class="editor-select field-type-select"
					value={item.type}
					onchange={(e) => handleTypeChange((e.target as HTMLSelectElement).value as PlaylistItem['type'])}
				>
					<option value="clip">Clip</option>
					<option value="live">Live</option>
					<option value="slate">Slate</option>
				</select>
			</label>
			<label class="editor-field editor-field-grow">
				<span class="editor-label">Title</span>
				<input
					class="editor-input field-title"
					type="text"
					value={item.title}
					oninput={(e) => emit({ title: (e.target as HTMLInputElement).value })}
				/>
			</label>
		</div>
	</div>

	<!-- Type-specific fields -->
	<div class="editor-section">
		{#if item.type === 'clip'}
			<label class="editor-field editor-field-full">
				<span class="editor-label">Asset</span>
				{#if clips.length > 0}
					<select
						class="editor-select field-asset-select"
						value={item.asset_uri ?? ''}
						onchange={(e) => {
							const val = (e.target as HTMLSelectElement).value;
							if (val) {
								const match = clips.find(c => c.path === val);
								if (match) {
									emit({ asset_uri: val, title: item.title === 'New Item' ? match.name : item.title, duration_ms: match.duration * 1000 });
								} else {
									emit({ asset_uri: val });
								}
							} else {
								emit({ asset_uri: undefined });
							}
						}}
					>
						<option value="">Custom URI...</option>
						{#each clips as clip}
							<option value={clip.path}>{clip.name} ({Math.floor(clip.duration / 60)}:{String(clip.duration % 60).padStart(2, '0')})</option>
						{/each}
					</select>
				{/if}
				<input
					class="editor-input field-asset-uri"
					type="text"
					value={item.asset_uri ?? ''}
					oninput={(e) => emit({ asset_uri: (e.target as HTMLInputElement).value || undefined })}
					placeholder="/media/clip.ts"
				/>
			</label>

			<div class="editor-row">
				<label class="editor-field">
					<span class="editor-label">Duration</span>
					<div class="editor-with-unit">
						<input
							class="editor-input field-duration"
							type="number"
							min="0"
							value={item.duration_ms ?? ''}
							oninput={(e) => {
								const v = parseInt((e.target as HTMLInputElement).value);
								emit({ duration_ms: Number.isNaN(v) ? undefined : v });
							}}
						/>
						<span class="editor-unit">ms</span>
					</div>
				</label>
			</div>

			<div class="editor-row">
				<label class="editor-field">
					<span class="editor-label">In Point</span>
					<div class="editor-with-unit">
						<input
							class="editor-input field-in-point"
							type="number"
							min="0"
							value={item.in_point_ms ?? ''}
							oninput={(e) => {
								const v = parseInt((e.target as HTMLInputElement).value);
								emit({ in_point_ms: Number.isNaN(v) ? undefined : v });
							}}
						/>
						<span class="editor-unit">ms</span>
					</div>
				</label>
				<label class="editor-field">
					<span class="editor-label">Out Point</span>
					<div class="editor-with-unit">
						<input
							class="editor-input field-out-point"
							type="number"
							min="0"
							value={item.out_point_ms ?? ''}
							oninput={(e) => {
								const v = parseInt((e.target as HTMLInputElement).value);
								emit({ out_point_ms: Number.isNaN(v) ? undefined : v });
							}}
						/>
						<span class="editor-unit">ms</span>
					</div>
				</label>
			</div>

			<div class="editor-row editor-row-checks">
				<label class="editor-check">
					<input
						class="field-loop"
						type="checkbox"
						checked={item.loop ?? false}
						onchange={(e) => emit({ loop: (e.target as HTMLInputElement).checked || undefined })}
					/>
					<span class="editor-check-label">Loop</span>
				</label>
				<label class="editor-check">
					<input
						class="field-hold"
						type="checkbox"
						checked={item.hold ?? false}
						onchange={(e) => emit({ hold: (e.target as HTMLInputElement).checked || undefined })}
					/>
					<span class="editor-check-label">Hold</span>
				</label>
			</div>

		{:else if item.type === 'live'}
			<label class="editor-field editor-field-full">
				<span class="editor-label">Source</span>
				<select
					class="editor-select field-source-select"
					value={item.source ?? ''}
					onchange={(e) => emit({ source: (e.target as HTMLSelectElement).value || undefined })}
				>
					<option value="" disabled>Select a source</option>
					{#each sourceOptions as src (src.key)}
						<option value={src.key} title={src.key}>{src.label}</option>
					{/each}
				</select>
			</label>

		{:else if item.type === 'slate'}
			<div class="editor-row">
				<label class="editor-field">
					<span class="editor-label">Duration</span>
					<div class="editor-with-unit">
						<input
							class="editor-input field-duration"
							type="number"
							min="0"
							value={item.duration_ms ?? ''}
							oninput={(e) => {
								const v = parseInt((e.target as HTMLInputElement).value);
								emit({ duration_ms: Number.isNaN(v) ? undefined : v });
							}}
						/>
						<span class="editor-unit">ms</span>
					</div>
				</label>
			</div>
		{/if}
	</div>

	<!-- Transition -->
	<div class="editor-section">
		<div class="editor-row">
			<label class="editor-field">
				<span class="editor-label">Transition</span>
				<select
					class="editor-select field-transition-type"
					value={currentTransitionType}
					onchange={(e) => handleTransitionTypeChange((e.target as HTMLSelectElement).value)}
				>
					{#each transitionTypes as t}
						<option value={t}>{t === 'cut' ? 'Cut (none)' : t.replace('_', ' ')}</option>
					{/each}
				</select>
			</label>
			{#if currentTransitionType !== 'cut'}
				<label class="editor-field">
					<span class="editor-label">Duration</span>
					<div class="editor-with-unit">
						<input
							class="editor-input field-transition-duration"
							type="number"
							min="0"
							max="5000"
							step="100"
							value={currentTransitionDuration}
							oninput={(e) => handleTransitionDurationChange(parseInt((e.target as HTMLInputElement).value) || 500)}
						/>
						<span class="editor-unit">ms</span>
					</div>
				</label>
			{/if}
		</div>
		{#if currentTransitionType === 'wipe'}
			<div class="editor-row">
				<div class="wipe-config-section">
					<WipePatternBrowser config={itemWipeConfig} onConfigChange={handleWipeConfigChange} />
				</div>
			</div>
		{/if}
	</div>

	<!-- Secondary Events -->
	<div class="editor-section editor-section-events">
		<SecondaryEventEditor
			events={item.secondary_events ?? []}
			onUpdate={handleSecondaryEventsUpdate}
			itemDurationMs={item.duration_ms}
			{graphicsLayers}
		/>
	</div>
</div>

<style>
	.item-editor {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.editor-section {
		padding: 6px 8px;
		border-top: 1px solid var(--border-default);
	}

	.editor-section:first-child {
		border-top: none;
	}

	.editor-section-events {
		padding: 4px 0 2px;
	}

	.editor-row {
		display: flex;
		align-items: flex-end;
		gap: 8px;
		margin-bottom: 4px;
	}

	.editor-row:last-child {
		margin-bottom: 0;
	}

	.editor-row-checks {
		align-items: center;
		gap: 12px;
		margin-top: 2px;
	}

	.editor-field {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 80px;
	}

	.editor-field-grow {
		flex: 1;
		min-width: 0;
	}

	.editor-field-full {
		display: flex;
		flex-direction: column;
		gap: 2px;
		width: 100%;
		margin-bottom: 4px;
	}

	.editor-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.editor-select,
	.editor-input {
		background: rgba(0, 0, 0, 0.2);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: 10px;
		padding: 3px 5px;
		outline: none;
		width: 100%;
	}

	.editor-select:focus,
	.editor-input:focus {
		border-color: var(--accent-blue);
	}

	.editor-input[type='number'] {
		width: 72px;
	}

	.editor-with-unit {
		display: flex;
		align-items: center;
		gap: 3px;
	}

	.editor-unit {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.editor-check {
		display: flex;
		align-items: center;
		gap: 4px;
		cursor: pointer;
	}

	.editor-check input[type='checkbox'] {
		accent-color: var(--accent-blue);
	}

	.editor-check-label {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.wipe-config-section {
		width: 100%;
		padding: 4px 0;
	}
</style>

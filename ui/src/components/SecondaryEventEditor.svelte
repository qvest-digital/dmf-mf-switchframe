<script lang="ts">
	import type { SecondaryEvent } from '$lib/api/types';

	interface Props {
		events: SecondaryEvent[];
		onUpdate: (events: SecondaryEvent[]) => void;
		itemDurationMs?: number;
	}

	let { events, onUpdate, itemDurationMs }: Props = $props();

	let expanded = $state(false);
	let editingIndex = $state<number | null>(null);

	const eventTypeLabels: Record<string, string> = {
		graphics_on: 'GFX On',
		graphics_off: 'GFX Off',
		graphics_update: 'GFX Update',
		audio_level: 'Audio Level',
		audio_mute: 'Audio Mute',
		audio_afv: 'Audio AFV',
		caption_mode: 'Caption Mode',
		caption_text: 'Caption Text',
		scte35_cue: 'SCTE-35 Cue',
	};

	const eventTypeColors: Record<string, string> = {
		graphics_on: 'var(--accent-green)',
		graphics_off: 'var(--accent-green)',
		graphics_update: 'var(--accent-green)',
		audio_level: 'var(--accent-blue)',
		audio_mute: 'var(--accent-blue)',
		audio_afv: 'var(--accent-blue)',
		caption_mode: 'var(--accent-orange)',
		caption_text: 'var(--accent-orange)',
		scte35_cue: 'var(--tally-program)',
	};

	const eventTypes: SecondaryEvent['type'][] = [
		'graphics_on', 'graphics_off', 'graphics_update',
		'audio_level', 'audio_mute', 'audio_afv',
		'caption_mode', 'caption_text', 'scte35_cue',
	];

	function addEvent() {
		const newEvent: SecondaryEvent = {
			type: 'graphics_on',
			offset_ms: 0,
			anchor: 'start',
		};
		onUpdate([...events, newEvent]);
		editingIndex = events.length;
		expanded = true;
	}

	function removeEvent(idx: number) {
		const updated = events.filter((_, i) => i !== idx);
		onUpdate(updated);
		if (editingIndex === idx) editingIndex = null;
		else if (editingIndex !== null && editingIndex > idx) editingIndex--;
	}

	function updateEvent(idx: number, patch: Partial<SecondaryEvent>) {
		const updated = events.map((e, i) => (i === idx ? { ...e, ...patch } : e));
		onUpdate(updated);
	}

	function formatOffset(evt: SecondaryEvent): string {
		const sign = evt.offset_ms >= 0 ? '+' : '';
		const secs = (evt.offset_ms / 1000).toFixed(1);
		return `${evt.anchor === 'start' ? 'S' : 'E'}${sign}${secs}s`;
	}

	function timelinePosition(evt: SecondaryEvent): number {
		if (!itemDurationMs || itemDurationMs <= 0) return 0;
		const abs = evt.anchor === 'start' ? evt.offset_ms : itemDurationMs + evt.offset_ms;
		return Math.max(0, Math.min(100, (abs / itemDurationMs) * 100));
	}

	function isGraphicsType(type: string): boolean {
		return type === 'graphics_on' || type === 'graphics_off' || type === 'graphics_update';
	}

	function isAudioType(type: string): boolean {
		return type === 'audio_level' || type === 'audio_mute' || type === 'audio_afv';
	}

	function isCaptionType(type: string): boolean {
		return type === 'caption_mode' || type === 'caption_text';
	}
</script>

<div class="sec-events">
	<button class="sec-header" onclick={() => (expanded = !expanded)}>
		<span class="sec-chevron">{expanded ? '\u25BE' : '\u25B8'}</span>
		<span class="sec-label">Events ({events.length})</span>
	</button>

	{#if expanded}
		<div class="sec-list">
			{#each events as evt, idx}
				<div class="sec-row">
					<span class="sec-type-dot" style="background: {eventTypeColors[evt.type] ?? 'var(--text-tertiary)'}"></span>
					<span class="sec-type-label">{eventTypeLabels[evt.type] ?? evt.type}</span>
					<span class="sec-offset">{formatOffset(evt)}</span>
					<button class="sec-btn sec-edit-btn" onclick={() => (editingIndex = editingIndex === idx ? null : idx)}>
						{editingIndex === idx ? 'Done' : 'Edit'}
					</button>
					<button class="sec-btn sec-del-btn" onclick={() => removeEvent(idx)}>&times;</button>
				</div>

				{#if editingIndex === idx}
					<div class="sec-editor">
						<label class="sec-field">
							<span class="sec-field-label">Type</span>
							<select
								class="sec-select"
								value={evt.type}
								onchange={(e) => updateEvent(idx, { type: (e.target as HTMLSelectElement).value as SecondaryEvent['type'] })}
							>
								{#each eventTypes as t}
									<option value={t}>{eventTypeLabels[t]}</option>
								{/each}
							</select>
						</label>

						<label class="sec-field">
							<span class="sec-field-label">Anchor</span>
							<select
								class="sec-select"
								value={evt.anchor}
								onchange={(e) => updateEvent(idx, { anchor: (e.target as HTMLSelectElement).value as 'start' | 'end' })}
							>
								<option value="start">Start</option>
								<option value="end">End</option>
							</select>
						</label>

						<label class="sec-field">
							<span class="sec-field-label">Offset (ms)</span>
							<input
								class="sec-input"
								type="number"
								value={evt.offset_ms}
								onchange={(e) => updateEvent(idx, { offset_ms: parseInt((e.target as HTMLInputElement).value) || 0 })}
							/>
						</label>

						{#if isGraphicsType(evt.type)}
							<label class="sec-field">
								<span class="sec-field-label">Layer</span>
								<input
									class="sec-input"
									type="number"
									min="0"
									value={evt.layer ?? 0}
									onchange={(e) => updateEvent(idx, { layer: parseInt((e.target as HTMLInputElement).value) || 0 })}
								/>
							</label>
							{#if evt.type === 'graphics_on' || evt.type === 'graphics_update'}
								<label class="sec-field">
									<span class="sec-field-label">Template</span>
									<input
										class="sec-input"
										type="text"
										value={evt.template ?? ''}
										onchange={(e) => updateEvent(idx, { template: (e.target as HTMLInputElement).value || undefined })}
									/>
								</label>
							{/if}
						{/if}

						{#if isAudioType(evt.type)}
							<label class="sec-field">
								<span class="sec-field-label">Source Key</span>
								<input
									class="sec-input"
									type="text"
									value={evt.source_key ?? ''}
									onchange={(e) => updateEvent(idx, { source_key: (e.target as HTMLInputElement).value || undefined })}
								/>
							</label>
							{#if evt.type === 'audio_level'}
								<label class="sec-field">
									<span class="sec-field-label">Level (dB)</span>
									<input
										class="sec-input"
										type="number"
										step="0.1"
										value={evt.level_db ?? 0}
										onchange={(e) => updateEvent(idx, { level_db: parseFloat((e.target as HTMLInputElement).value) || 0 })}
									/>
								</label>
							{/if}
							{#if evt.type === 'audio_mute'}
								<label class="sec-field sec-field-row">
									<input
										type="checkbox"
										checked={evt.muted ?? false}
										onchange={(e) => updateEvent(idx, { muted: (e.target as HTMLInputElement).checked })}
									/>
									<span class="sec-field-label">Muted</span>
								</label>
							{/if}
							{#if evt.type === 'audio_afv'}
								<label class="sec-field sec-field-row">
									<input
										type="checkbox"
										checked={evt.afv ?? false}
										onchange={(e) => updateEvent(idx, { afv: (e.target as HTMLInputElement).checked })}
									/>
									<span class="sec-field-label">AFV</span>
								</label>
							{/if}
						{/if}

						{#if isCaptionType(evt.type)}
							{#if evt.type === 'caption_mode'}
								<label class="sec-field">
									<span class="sec-field-label">Mode</span>
									<select
										class="sec-select"
										value={evt.caption_mode ?? 'off'}
										onchange={(e) => updateEvent(idx, { caption_mode: (e.target as HTMLSelectElement).value })}
									>
										<option value="off">Off</option>
										<option value="passthrough">Passthrough</option>
										<option value="author">Author</option>
										<option value="auto">Auto</option>
									</select>
								</label>
							{/if}
							{#if evt.type === 'caption_text'}
								<label class="sec-field">
									<span class="sec-field-label">Text</span>
									<input
										class="sec-input"
										type="text"
										value={evt.caption_text ?? ''}
										onchange={(e) => updateEvent(idx, { caption_text: (e.target as HTMLInputElement).value })}
									/>
								</label>
							{/if}
						{/if}

						{#if evt.type === 'scte35_cue'}
							<label class="sec-field">
								<span class="sec-field-label">Break Duration (ms)</span>
								<input
									class="sec-input"
									type="number"
									value={evt.scte35?.break_duration_ms ?? 0}
									onchange={(e) => updateEvent(idx, { scte35: { ...evt.scte35, break_duration_ms: parseInt((e.target as HTMLInputElement).value) || 0 } })}
								/>
							</label>
							<label class="sec-field sec-field-row">
								<input
									type="checkbox"
									checked={evt.scte35?.auto_return ?? false}
									onchange={(e) => updateEvent(idx, { scte35: { ...evt.scte35, auto_return: (e.target as HTMLInputElement).checked } })}
								/>
								<span class="sec-field-label">Auto Return</span>
							</label>
						{/if}
					</div>
				{/if}
			{/each}

			<button class="sec-add-btn" onclick={addEvent}>+ Add Event</button>
		</div>

		{#if itemDurationMs && itemDurationMs > 0 && events.length > 0}
			<div class="sec-timeline">
				<div class="sec-timeline-bar">
					{#each events as evt}
						<div
							class="sec-timeline-marker"
							style="left: {timelinePosition(evt)}%; background: {eventTypeColors[evt.type] ?? 'var(--text-tertiary)'}"
							title="{eventTypeLabels[evt.type]} @ {formatOffset(evt)}"
						></div>
					{/each}
				</div>
			</div>
		{/if}
	{/if}
</div>

<style>
	.sec-events {
		padding: 0 5px 3px 23px;
	}

	.sec-header {
		display: flex;
		align-items: center;
		gap: 4px;
		background: none;
		border: none;
		padding: 1px 0;
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 8px;
		color: var(--text-tertiary);
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.sec-header:hover {
		color: var(--text-secondary);
	}

	.sec-chevron {
		font-size: 9px;
		line-height: 1;
	}

	.sec-label {
		/* inherits from header */
	}

	.sec-list {
		display: flex;
		flex-direction: column;
		gap: 1px;
		margin-top: 2px;
	}

	.sec-row {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 4px;
		border-radius: 2px;
	}

	.sec-row:hover {
		background: var(--bg-hover);
	}

	.sec-type-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.sec-type-label {
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-secondary);
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.sec-offset {
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.sec-btn {
		background: none;
		border: 1px solid var(--border-subtle);
		border-radius: 2px;
		padding: 0 4px;
		font-family: var(--font-ui);
		font-size: 8px;
		cursor: pointer;
		line-height: 1.6;
	}

	.sec-edit-btn {
		color: var(--text-tertiary);
	}

	.sec-edit-btn:hover {
		color: var(--accent-blue);
		border-color: var(--accent-blue);
	}

	.sec-del-btn {
		color: var(--text-tertiary);
		font-size: 10px;
		line-height: 1.2;
		padding: 0 3px;
	}

	.sec-del-btn:hover {
		color: var(--tally-program);
		border-color: var(--tally-program);
	}

	.sec-editor {
		display: flex;
		flex-wrap: wrap;
		gap: 4px 8px;
		padding: 4px 4px 6px 10px;
		margin: 1px 0 3px;
		border-left: 2px solid var(--border-subtle);
	}

	.sec-field {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 80px;
	}

	.sec-field-row {
		flex-direction: row;
		align-items: center;
		gap: 4px;
	}

	.sec-field-label {
		font-family: var(--font-ui);
		font-size: 8px;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.sec-select,
	.sec-input {
		background: rgba(0, 0, 0, 0.2);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: 9px;
		padding: 2px 4px;
		outline: none;
	}

	.sec-select:focus,
	.sec-input:focus {
		border-color: var(--accent-blue);
	}

	.sec-input[type='number'] {
		width: 64px;
	}

	.sec-input[type='text'] {
		width: 100px;
	}

	.sec-add-btn {
		margin-top: 2px;
		background: none;
		border: 1px dashed var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 8px;
		padding: 2px 8px;
		cursor: pointer;
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.sec-add-btn:hover {
		color: var(--accent-blue);
		border-color: var(--accent-blue);
	}

	.sec-timeline {
		margin-top: 4px;
		padding: 0 4px;
	}

	.sec-timeline-bar {
		position: relative;
		height: 6px;
		background: rgba(255, 255, 255, 0.05);
		border-radius: 3px;
		overflow: visible;
	}

	.sec-timeline-marker {
		position: absolute;
		top: -1px;
		width: 4px;
		height: 8px;
		border-radius: 1px;
		transform: translateX(-2px);
	}
</style>

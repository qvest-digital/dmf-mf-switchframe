<script lang="ts">
	import type { Playlist, PodTemplateDef, SampleClip } from '$lib/api/types';
	import { ICON_CHEVRON_DOWN, ICON_CHEVRON_RIGHT, ICON_PLUS, ICON_CLOSE, renderIcon } from '$lib/icons';

	interface Props {
		playlist: Playlist;
		onChange: (updated: Playlist) => void;
		sampleClips?: SampleClip[];
	}

	let { playlist, onChange, sampleClips = [] }: Props = $props();

	let expanded = $state(false);

	const transitionTypes = ['cut', 'dissolve', 'dip_black', 'wipe'] as const;

	// Idle asset mode derived from current playlist state
	const idleMode = $derived<'none' | 'clip' | 'custom'>(
		!playlist.idle_asset ? 'none'
			: sampleClips.some((c) => c.path === playlist.idle_asset) ? 'clip'
			: 'custom'
	);

	function updateField<K extends keyof Playlist>(key: K, value: Playlist[K]) {
		onChange({ ...playlist, [key]: value });
	}

	function updateTransitionType(type: string) {
		const dur = type === 'cut' ? undefined : (playlist.default_transition.duration_ms ?? 500);
		onChange({
			...playlist,
			default_transition: { type, duration_ms: dur },
		});
	}

	function updateTransitionDuration(ms: number) {
		onChange({
			...playlist,
			default_transition: { ...playlist.default_transition, duration_ms: ms },
		});
	}

	function setIdleMode(mode: string) {
		if (mode === 'none') {
			onChange({ ...playlist, idle_asset: undefined });
		} else if (mode === 'clip' && sampleClips.length > 0) {
			onChange({ ...playlist, idle_asset: sampleClips[0].path });
		} else if (mode === 'custom') {
			onChange({ ...playlist, idle_asset: '' });
		}
	}

	function setIdleAssetClip(path: string) {
		onChange({ ...playlist, idle_asset: path });
	}

	function setIdleAssetCustom(uri: string) {
		onChange({ ...playlist, idle_asset: uri });
	}

	function addPodTemplate() {
		const newPod: PodTemplateDef = {
			label: 'New Break',
			duration_ms: 60000,
			auto_return: true,
		};
		onChange({ ...playlist, pods: [...(playlist.pods ?? []), newPod] });
	}

	function removePodTemplate(idx: number) {
		const updated = (playlist.pods ?? []).filter((_, i) => i !== idx);
		onChange({ ...playlist, pods: updated });
	}

	function updatePodTemplate(idx: number, patch: Partial<PodTemplateDef>) {
		const updated = (playlist.pods ?? []).map((p, i) => (i === idx ? { ...p, ...patch } : p));
		onChange({ ...playlist, pods: updated });
	}
</script>

<div class="settings-section">
	<button type="button" class="section-toggle" onclick={() => (expanded = !expanded)}>
		<span class="section-chevron" aria-hidden="true">
			{@html renderIcon(expanded ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
		</span>
		<span class="section-toggle-label">Playlist Settings</span>
		{#if playlist.pods?.length}
			<span class="section-badge">{playlist.pods.length} pods</span>
		{/if}
	</button>

	{#if expanded}
		<div class="section-body">
			<!-- Playlist Info -->
			<div class="field-group">
				<label class="field">
					<span class="field-label">Name</span>
					<input
						class="field-input"
						type="text"
						value={playlist.name}
						onchange={(e) => updateField('name', (e.target as HTMLInputElement).value)}
					/>
				</label>
				<div class="field">
					<span class="field-label">ID</span>
					<span class="field-readonly">{playlist.id}</span>
				</div>
			</div>

			<!-- Idle Asset -->
			<div class="field-group">
				<label class="field">
					<span class="field-label">Idle Asset</span>
					<select
						class="field-select"
						value={idleMode}
						onchange={(e) => setIdleMode((e.target as HTMLSelectElement).value)}
					>
						<option value="none">None</option>
						{#each sampleClips as clip}
							<option value="clip">{clip.title}</option>
						{/each}
						<option value="custom">Custom URI</option>
					</select>
				</label>

				{#if idleMode === 'clip' && sampleClips.length > 1}
					<label class="field">
						<span class="field-label">Clip</span>
						<select
							class="field-select"
							value={playlist.idle_asset}
							onchange={(e) => setIdleAssetClip((e.target as HTMLSelectElement).value)}
						>
							{#each sampleClips as clip}
								<option value={clip.path}>{clip.title}</option>
							{/each}
						</select>
					</label>
				{/if}

				{#if idleMode === 'custom'}
					<label class="field">
						<span class="field-label">URI</span>
						<input
							class="field-input field-input-wide"
							type="text"
							value={playlist.idle_asset ?? ''}
							placeholder="file:///path/to/slate.ts"
							onchange={(e) => setIdleAssetCustom((e.target as HTMLInputElement).value)}
						/>
					</label>
				{/if}

				{#if idleMode !== 'none'}
					<label class="field field-row">
						<input
							type="checkbox"
							checked={playlist.idle_loop ?? true}
							onchange={(e) => updateField('idle_loop', (e.target as HTMLInputElement).checked)}
						/>
						<span class="field-label">Loop idle asset</span>
					</label>
				{/if}
			</div>

			<!-- Default Transition -->
			<div class="field-group">
				<label class="field">
					<span class="field-label">Default Transition</span>
					<select
						class="field-select"
						value={playlist.default_transition.type}
						onchange={(e) => updateTransitionType((e.target as HTMLSelectElement).value)}
					>
						{#each transitionTypes as t}
							<option value={t}>{t.replace('_', ' ')}</option>
						{/each}
					</select>
				</label>

				{#if playlist.default_transition.type !== 'cut'}
					<label class="field">
						<span class="field-label">Duration (ms)</span>
						<input
							class="field-input field-input-num"
							type="number"
							min="0"
							step="100"
							value={playlist.default_transition.duration_ms ?? 500}
							onchange={(e) => updateTransitionDuration(parseInt((e.target as HTMLInputElement).value) || 500)}
						/>
					</label>
				{/if}
			</div>

			<!-- Pod Templates -->
			<div class="field-group">
				<span class="field-label">Pod Templates</span>
				{#each playlist.pods ?? [] as pod, idx}
					<div class="pod-row">
						<input
							class="field-input pod-label"
							type="text"
							value={pod.label}
							placeholder="Label"
							onchange={(e) => updatePodTemplate(idx, { label: (e.target as HTMLInputElement).value })}
						/>
						<input
							class="field-input field-input-num"
							type="number"
							min="0"
							step="1000"
							value={pod.duration_ms}
							onchange={(e) => updatePodTemplate(idx, { duration_ms: parseInt((e.target as HTMLInputElement).value) || 0 })}
						/>
						<span class="pod-ms-suffix">ms</span>
						<label class="pod-auto">
							<input
								type="checkbox"
								checked={pod.auto_return}
								onchange={(e) => updatePodTemplate(idx, { auto_return: (e.target as HTMLInputElement).checked })}
							/>
							<span class="pod-auto-label">auto</span>
						</label>
						<button
							type="button"
							class="pod-remove-btn"
							title="Remove template"
							onclick={() => removePodTemplate(idx)}
						>
							{@html renderIcon(ICON_CLOSE, 10)}
						</button>
					</div>
				{/each}

				<button type="button" class="pod-add-btn" onclick={addPodTemplate}>
					{@html renderIcon(ICON_PLUS, 10)}
					<span>Add Template</span>
				</button>
			</div>
		</div>
	{/if}
</div>

<style>
	.settings-section {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		overflow: hidden;
		flex-shrink: 0;
	}

	.section-toggle {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 5px 8px;
		border: none;
		background: var(--bg-control);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		cursor: pointer;
		text-align: left;
		transition: background var(--transition-fast);
	}

	.section-toggle:hover {
		background: var(--bg-hover);
	}

	.section-chevron {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.section-toggle-label {
		flex: 1;
	}

	.section-badge {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		background: var(--bg-base);
		padding: 0 4px;
		border-radius: var(--radius-sm);
	}

	.section-body {
		padding: 6px 8px;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.field-group {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}

	.field {
		display: flex;
		flex-direction: column;
		gap: 1px;
	}

	.field-row {
		flex-direction: row;
		align-items: center;
		gap: 4px;
	}

	.field-label {
		font-family: var(--font-ui);
		font-size: 8px;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.field-readonly {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		padding: 2px 0;
		user-select: all;
	}

	.field-select,
	.field-input {
		background: rgba(0, 0, 0, 0.2);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-family: var(--font-mono);
		font-size: 9px;
		padding: 2px 4px;
		outline: none;
	}

	.field-select:focus,
	.field-input:focus {
		border-color: var(--accent-blue);
	}

	.field-input-num {
		width: 72px;
	}

	.field-input-wide {
		width: 100%;
	}

	/* Pod template rows */

	.pod-row {
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.pod-label {
		flex: 1;
		min-width: 80px;
	}

	.pod-ms-suffix {
		font-family: var(--font-mono);
		font-size: 8px;
		color: var(--text-tertiary);
	}

	.pod-auto {
		display: flex;
		align-items: center;
		gap: 2px;
		cursor: pointer;
	}

	.pod-auto-label {
		font-family: var(--font-ui);
		font-size: 8px;
		color: var(--text-tertiary);
		text-transform: uppercase;
	}

	.pod-remove-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 18px;
		height: 18px;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--text-tertiary);
		cursor: pointer;
		padding: 0;
		transition: color var(--transition-fast), border-color var(--transition-fast);
	}

	.pod-remove-btn:hover {
		color: var(--color-error);
		border-color: var(--color-error);
	}

	.pod-add-btn {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 3px 6px;
		border: 1px dashed var(--border-subtle);
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: 9px;
		cursor: pointer;
		transition: color var(--transition-fast), border-color var(--transition-fast);
	}

	.pod-add-btn:hover {
		color: var(--text-secondary);
		border-color: var(--text-secondary);
	}
</style>

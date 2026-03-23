<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';
	import { setColorGrade, uploadColorGradeLUT, deleteColorGradeLUT } from '$lib/api/switch-api';
	import { notify } from '$lib/state/notifications.svelte';
	import {
		ICON_CHECK,
		ICON_CHEVRON_DOWN,
		ICON_CHEVRON_RIGHT,
		ICON_CLOSE,
		ICON_UPLOAD,
		renderIcon,
	} from '$lib/icons';

	let { state: crState }: { state: ControlRoomState } = $props();

	let colorGrade = $derived(crState.colorGrade);
	let activeLook = $derived(colorGrade?.lookName ?? '');
	let intensity = $state(100);
	let selectedGroup = $state<string>('All');
	let uploading = $state(false);
	let lutOpen = $state(true);
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	// Get unique groups from available looks, preserving a stable order
	let groups = $derived.by(() => {
		if (!colorGrade?.available) return ['All'];
		const seen = new Set<string>();
		for (const look of colorGrade.available) {
			seen.add(look.group);
		}
		return ['All', ...Array.from(seen)];
	});

	// Filter looks by selected group
	let filteredLooks = $derived.by(() => {
		if (!colorGrade?.available) return [];
		if (selectedGroup === 'All') return colorGrade.available;
		return colorGrade.available.filter(l => l.group === selectedGroup);
	});

	// Custom (non-builtin) looks for the upload section
	let customLooks = $derived.by(() => {
		if (!colorGrade?.available) return [];
		return colorGrade.available.filter(l => !l.builtin);
	});

	// Guard against server state overwriting in-progress user edits.
	let lastUserEdit = 0;
	const SYNC_GUARD_MS = 1000;

	// Sync intensity from server state (skipped while user is editing)
	$effect(() => {
		if (colorGrade) {
			if (Date.now() - lastUserEdit < SYNC_GUARD_MS) return;
			intensity = Math.round(colorGrade.intensity * 100);
		}
	});

	function formatLookName(name: string): string {
		return name
			.replace(/-/g, ' ')
			.replace(/\b\w/g, c => c.toUpperCase());
	}

	async function applyLook(name: string) {
		try {
			await setColorGrade({ lookName: name, intensity: intensity / 100 });
		} catch (err) {
			notify('error', `Apply failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function disable() {
		try {
			await setColorGrade({ enabled: false });
		} catch (err) {
			notify('error', `Disable failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function updateIntensity() {
		lastUserEdit = Date.now();
		if (!colorGrade?.enabled || !activeLook) return;
		try {
			await setColorGrade({ lookName: activeLook, intensity: intensity / 100 });
		} catch (err) {
			notify('error', `Update failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}

	async function handleUpload() {
		const file = fileInput?.files?.[0];
		if (!file) return;

		const name = file.name.replace(/\.cube$/i, '').replace(/[^a-zA-Z0-9_-]/g, '-');
		if (!name) {
			notify('error', 'Invalid file name');
			return;
		}

		uploading = true;
		try {
			await uploadColorGradeLUT(name, file);
			notify('info', `Uploaded "${name}"`);
		} catch (err) {
			notify('error', `Upload failed: ${err instanceof Error ? err.message : 'unknown'}`);
		} finally {
			uploading = false;
			if (fileInput) fileInput.value = '';
		}
	}

	async function handleDelete(name: string) {
		try {
			await deleteColorGradeLUT(name);
			notify('info', `Deleted "${name}"`);
		} catch (err) {
			notify('error', `Delete failed: ${err instanceof Error ? err.message : 'unknown'}`);
		}
	}
</script>

<div class="looks-tab">
	<!-- Filter row: Off first, then group pills -->
	<div class="group-bar">
		{#if colorGrade?.enabled}
			<button type="button" class="pill pill-off" onclick={disable}>Off</button>
		{/if}
		{#each groups as group}
			<button
				type="button"
				class="pill"
				class:pill-active={selectedGroup === group}
				onclick={() => (selectedGroup = group)}
			>{group}</button>
		{/each}
	</div>

	<!-- Intensity + active look name (only when a look is on-air) -->
	{#if colorGrade?.enabled && activeLook}
		<div class="intensity-row">
			<span class="active-look-label" title={formatLookName(activeLook)}>{formatLookName(activeLook)}</span>
			<div class="intensity-inline">
				<span class="intensity-pct">{intensity}%</span>
				<input
					type="range"
					min="0"
					max="100"
					step="1"
					bind:value={intensity}
					onchange={updateIntensity}
					class="intensity-slider"
					aria-label="Look intensity"
				/>
			</div>
		</div>
	{/if}

	<div class="looks-grid-scroll">
		<div class="looks-grid">
			{#if filteredLooks.length > 0}
				{#each filteredLooks as look}
					<button
						type="button"
						class="look-card"
						class:look-card-active={colorGrade?.enabled && activeLook === look.name}
						onclick={() => applyLook(look.name)}
					>
						{#if colorGrade?.enabled && activeLook === look.name}
							<span class="look-check-badge" aria-hidden="true">
								{@html renderIcon(ICON_CHECK, 10)}
							</span>
						{/if}
						<div class="look-info">
							<span class="look-name">{formatLookName(look.name)}</span>
							{#if look.description}
								<span class="look-desc">{look.description}</span>
							{/if}
						</div>
					</button>
				{/each}
			{:else}
				<span class="no-looks">No looks available</span>
			{/if}
		</div>
	</div>

	<!-- Custom LUT: collapsible -->
	<div class="lut-section">
		<button type="button" class="lut-toggle" onclick={() => (lutOpen = !lutOpen)}>
			<span class="lut-chevron" aria-hidden="true">
				{@html renderIcon(lutOpen ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 12)}
			</span>
			<span class="lut-toggle-title">Custom LUT</span>
			{#if customLooks.length > 0}
				<span class="lut-badge">{customLooks.length}</span>
			{/if}
		</button>
		{#if lutOpen}
			<div class="lut-body">
				<div class="upload-row">
					<span class="upload-icon" aria-hidden="true">{@html renderIcon(ICON_UPLOAD, 14)}</span>
					<input
						type="file"
						accept=".cube"
						bind:this={fileInput}
						onchange={handleUpload}
						class="file-input"
						disabled={uploading}
					/>
					{#if uploading}
						<span class="uploading-label">Uploading…</span>
					{/if}
				</div>

				{#if customLooks.length > 0}
					<div class="custom-looks">
						{#each customLooks as look}
							<div class="custom-look-item">
								<span class="custom-look-name">{formatLookName(look.name)}</span>
								<button
									type="button"
									class="btn-delete"
									onclick={() => handleDelete(look.name)}
									title="Delete"
									aria-label="Delete {formatLookName(look.name)}"
								>
									{@html renderIcon(ICON_CLOSE, 12)}
								</button>
							</div>
						{/each}
					</div>
				{/if}
			</div>
		{/if}
	</div>
</div>

<style>
	.looks-tab {
		display: flex;
		flex-direction: column;
		height: 100%;
		overflow: hidden;
		padding: 6px 8px;
		gap: 6px;
		font-family: var(--font-ui, sans-serif);
	}

	.group-bar {
		display: flex;
		flex-direction: row;
		align-items: center;
		gap: 4px;
		flex-shrink: 0;
		overflow-x: auto;
	}

	.pill {
		padding: 2px 7px;
		background: var(--bg-control, var(--bg-surface, #2a2a2a));
		border: 1px solid var(--border-default, var(--border-subtle, #444));
		border-radius: var(--radius-sm, 4px);
		color: var(--text-secondary, #999);
		cursor: pointer;
		font-family: var(--font-ui, sans-serif);
		font-size: var(--text-2xs, 10px);
		font-weight: 500;
		white-space: nowrap;
		transition:
			background var(--transition-fast, 0.12s ease),
			border-color var(--transition-fast, 0.12s ease),
			color var(--transition-fast, 0.12s ease);
		flex-shrink: 0;
	}

	.pill:hover {
		background: var(--bg-hover, #333);
		color: var(--text-primary, #eee);
	}

	.pill-active {
		background: color-mix(in srgb, var(--accent-yellow, #d4a017) 12%, var(--bg-elevated, #222));
		border-color: var(--accent-yellow, #d4a017);
		color: var(--accent-yellow, #d4a017);
	}

	.pill-off {
		color: var(--text-tertiary, #888);
	}

	.intensity-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-shrink: 0;
		min-width: 0;
	}

	.active-look-label {
		font-size: var(--text-xs, 11px);
		font-weight: 600;
		color: var(--text-primary, #eee);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 42%;
		flex-shrink: 0;
	}

	.intensity-inline {
		display: flex;
		align-items: center;
		gap: 6px;
		flex: 1;
		min-width: 0;
	}

	.intensity-pct {
		font-size: var(--text-2xs, 10px);
		font-variant-numeric: tabular-nums;
		color: var(--text-secondary, #999);
		width: 2.25rem;
		flex-shrink: 0;
		text-align: right;
	}

	.intensity-slider {
		flex: 1;
		min-width: 0;
		height: 4px;
		accent-color: var(--accent-yellow, #d4a017);
	}

	.looks-grid-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
	}

	.looks-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(130px, 1fr));
		gap: 4px;
		align-content: start;
	}

	.look-card {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: stretch;
		padding: 5px 8px;
		min-height: 2.75rem;
		background: var(--bg-surface, #2a2a2a);
		border: 1px solid var(--border-default, var(--border-subtle, #444));
		border-radius: var(--radius-sm, 4px);
		color: var(--text-primary, #eee);
		cursor: pointer;
		font-family: var(--font-ui, sans-serif);
		text-align: left;
		transition:
			background var(--transition-fast, 0.12s ease),
			border-color var(--transition-fast, 0.12s ease);
	}

	.look-card:hover {
		background: var(--bg-hover, #333);
		border-color: var(--border-default, #666);
	}

	.look-card-active {
		border-color: var(--accent-yellow, #d4a017);
		box-shadow: 0 0 0 1px color-mix(in srgb, var(--accent-yellow, #d4a017) 35%, transparent);
		background: color-mix(in srgb, var(--accent-yellow, #d4a017) 8%, var(--bg-surface, #2a2a2a));
	}

	.look-check-badge {
		position: absolute;
		top: 3px;
		right: 3px;
		display: flex;
		align-items: center;
		justify-content: center;
		width: 16px;
		height: 16px;
		border-radius: var(--radius-xs, 2px);
		background: color-mix(in srgb, var(--accent-yellow, #d4a017) 22%, var(--bg-base, #111));
		color: var(--accent-yellow, #d4a017);
		pointer-events: none;
	}

	.look-check-badge :global(svg) {
		display: block;
	}

	.look-info {
		display: flex;
		flex-direction: column;
		gap: 1px;
		min-width: 0;
		padding-right: 14px;
	}

	.look-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--text-xs, 11px);
		font-weight: 600;
		line-height: 1.2;
	}

	.look-desc {
		font-size: var(--text-2xs, 10px);
		color: var(--text-tertiary, #888);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		line-height: 1.25;
	}

	.look-card-active .look-desc {
		color: var(--text-secondary, #aaa);
	}

	.no-looks {
		grid-column: 1 / -1;
		color: var(--text-tertiary, #666);
		font-size: var(--text-xs, 11px);
		font-style: italic;
		padding: 4px 2px;
	}

	.lut-section {
		flex-shrink: 0;
		border: 1px solid var(--border-subtle, #333);
		border-radius: var(--radius-sm, 4px);
		background: var(--bg-elevated, #222);
		overflow: hidden;
	}

	.lut-toggle {
		display: flex;
		align-items: center;
		gap: 6px;
		width: 100%;
		padding: 4px 8px;
		background: transparent;
		border: none;
		cursor: pointer;
		user-select: none;
		text-align: left;
		font-family: var(--font-ui, sans-serif);
		transition: background var(--transition-fast, 0.12s ease);
	}

	.lut-toggle:hover {
		background: var(--bg-hover, #2a2a2a);
	}

	.lut-chevron {
		display: flex;
		color: var(--text-tertiary, #666);
		flex-shrink: 0;
	}

	.lut-chevron :global(svg) {
		display: block;
	}

	.lut-toggle-title {
		font-size: var(--text-2xs, 10px);
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--text-secondary, #999);
	}

	.lut-badge {
		margin-left: auto;
		font-family: var(--font-mono, monospace);
		font-size: var(--text-2xs, 10px);
		padding: 0 4px;
		border-radius: var(--radius-xs, 2px);
		background: var(--bg-control, #2a2a2a);
		border: 1px solid var(--border-subtle, #444);
		color: var(--text-secondary, #999);
	}

	.lut-body {
		padding: 0 8px 6px;
		display: flex;
		flex-direction: column;
		gap: 4px;
		border-top: 1px solid var(--border-subtle, #2a2a2a);
	}

	.upload-row {
		display: flex;
		align-items: center;
		gap: 6px;
		padding-top: 4px;
	}

	.upload-icon {
		display: flex;
		color: var(--text-tertiary, #666);
		flex-shrink: 0;
	}

	.upload-icon :global(svg) {
		display: block;
	}

	.file-input {
		flex: 1;
		min-width: 0;
		font-size: var(--text-2xs, 10px);
		color: var(--text-secondary, #999);
	}

	.file-input::file-selector-button {
		padding: 2px 6px;
		background: var(--bg-control, #2a2a2a);
		border: 1px solid var(--border-default, #444);
		border-radius: var(--radius-xs, 2px);
		color: var(--text-primary, #eee);
		cursor: pointer;
		font-family: var(--font-ui, sans-serif);
		font-size: var(--text-2xs, 10px);
		margin-right: 6px;
	}

	.uploading-label {
		font-size: var(--text-2xs, 10px);
		color: var(--accent-yellow, #d4a017);
		flex-shrink: 0;
	}

	.custom-looks {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.custom-look-item {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 6px;
		padding: 3px 6px;
		font-size: var(--text-2xs, 10px);
		color: var(--text-secondary, #999);
		background: var(--bg-base, #1a1a1a);
		border-radius: var(--radius-xs, 2px);
	}

	.custom-look-name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		min-width: 0;
	}

	.btn-delete {
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: none;
		color: var(--text-tertiary, #666);
		cursor: pointer;
		padding: 2px;
		flex-shrink: 0;
		border-radius: var(--radius-xs, 2px);
		transition:
			color var(--transition-fast, 0.12s ease),
			background var(--transition-fast, 0.12s ease);
	}

	.btn-delete :global(svg) {
		display: block;
	}

	.btn-delete:hover {
		color: var(--error, #e74c3c);
		background: var(--bg-hover, #333);
	}
</style>

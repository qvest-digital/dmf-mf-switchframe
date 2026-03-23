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
	let lutOpen = $state(false);
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	let groups = $derived.by(() => {
		if (!colorGrade?.available) return ['All'];
		const seen = new Set<string>();
		for (const look of colorGrade.available) {
			seen.add(look.group);
		}
		return ['All', ...Array.from(seen)];
	});

	let filteredLooks = $derived.by(() => {
		if (!colorGrade?.available) return [];
		if (selectedGroup === 'All') return colorGrade.available;
		return colorGrade.available.filter(l => l.group === selectedGroup);
	});

	let customLooks = $derived.by(() => {
		if (!colorGrade?.available) return [];
		return colorGrade.available.filter(l => !l.builtin);
	});

	let lastUserEdit = 0;
	const SYNC_GUARD_MS = 1000;

	$effect(() => {
		if (colorGrade) {
			if (Date.now() - lastUserEdit < SYNC_GUARD_MS) return;
			intensity = Math.round(colorGrade.intensity * 100);
		}
	});

	function formatLookName(name: string): string {
		return name.replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
	}

	const groupHues: Record<string, string> = {
		'Drama':     '20',
		'Film':      '35',
		'Clear':     '190',
		'Light':     '50',
		'Shadow':    '260',
		'Violet':    '280',
		'Vintage':   '30',
		'Technical': '0',
	};

	const nameHints: Record<string, string> = {
		'anamorphic':    'hsl(35, 50%, 55%)',
		'bleach-bypass': 'hsl(40, 10%, 65%)',
		'broadcast':     'hsl(210, 30%, 55%)',
		'candlelight':   'hsl(35, 70%, 55%)',
		'chestnut':      'hsl(20, 45%, 45%)',
		'clarity':       'hsl(200, 40%, 60%)',
		'cobalt':        'hsl(220, 55%, 50%)',
		'cross-process': 'hsl(130, 35%, 50%)',
		'daguerreotype': 'hsl(40, 15%, 55%)',
		'daylight':      'hsl(45, 55%, 60%)',
		'ektachrome':    'hsl(210, 40%, 55%)',
		'electric':      'hsl(270, 50%, 55%)',
		'ember':         'hsl(15, 60%, 50%)',
		'faded':         'hsl(30, 20%, 60%)',
		'frost':         'hsl(200, 50%, 70%)',
		'glacier':       'hsl(195, 55%, 55%)',
		'golden-hour':   'hsl(35, 65%, 55%)',
		'honey':         'hsl(40, 55%, 50%)',
		'kodachrome':    'hsl(25, 50%, 50%)',
		'matte':         'hsl(30, 15%, 55%)',
		'moonstone':     'hsl(220, 30%, 65%)',
		'natural':       'hsl(80, 30%, 50%)',
		'neon':          'hsl(310, 60%, 55%)',
		'noir':          'hsl(0, 0%, 45%)',
		'polaroid':      'hsl(45, 35%, 60%)',
		'portra':        'hsl(25, 40%, 55%)',
		'prism':         'hsl(290, 45%, 55%)',
		'punch':         'hsl(350, 55%, 50%)',
		'radiant':       'hsl(45, 60%, 55%)',
		'sahara':        'hsl(35, 50%, 50%)',
	};

	function lookAccent(name: string, group: string): string {
		if (nameHints[name]) return nameHints[name];
		const hue = groupHues[group] ?? '200';
		const hash = name.split('').reduce((a, c) => a + c.charCodeAt(0), 0);
		const satShift = (hash % 20) - 10;
		const lightShift = (hash % 10) - 5;
		return `hsl(${hue}, ${40 + satShift}%, ${50 + lightShift}%)`;
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
		if (!name) { notify('error', 'Invalid file name'); return; }
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
	<div class="top-bar">
		<div class="group-bar">
			{#if colorGrade?.enabled}
				<button type="button" class="pill pill-off" onclick={disable}>Off</button>
			{/if}
			{#each groups as group}
				<button type="button" class="pill" class:pill-active={selectedGroup === group} onclick={() => (selectedGroup = group)}>{group}</button>
			{/each}
		</div>
		{#if colorGrade?.enabled && activeLook}
			<div class="intensity-ctrl">
				<span class="active-name" title={formatLookName(activeLook)}>{formatLookName(activeLook)}</span>
				<input type="range" min="0" max="100" step="1" bind:value={intensity} onchange={updateIntensity} class="intensity-slider" aria-label="Look intensity" />
				<span class="intensity-val">{intensity}%</span>
			</div>
		{/if}
	</div>

	<div class="looks-scroll">
		<div class="looks-grid">
			{#if filteredLooks.length > 0}
				{#each filteredLooks as look}
					{@const isActive = colorGrade?.enabled && activeLook === look.name}
					{@const accent = lookAccent(look.name, look.group)}
					<button
						type="button"
						class="look-chip"
						class:active={isActive}
						style="--look-accent: {accent};"
						onclick={() => applyLook(look.name)}
						title={look.description || formatLookName(look.name)}
					>
						<span class="look-swatch" aria-hidden="true"></span>
						<span class="look-name">{formatLookName(look.name)}</span>
						{#if isActive}
							<span class="look-check">{@html renderIcon(ICON_CHECK, 9)}</span>
						{/if}
					</button>
				{/each}
			{:else}
				<span class="no-looks">No looks available</span>
			{/if}
		</div>
	</div>

	<div class="lut-section">
		<div class="lut-row">
			<button type="button" class="lut-toggle" onclick={() => (lutOpen = !lutOpen)}>
				<span class="lut-chevron" aria-hidden="true">{@html renderIcon(lutOpen ? ICON_CHEVRON_DOWN : ICON_CHEVRON_RIGHT, 11)}</span>
				<span class="lut-label">Custom LUT</span>
				{#if customLooks.length > 0}
					<span class="lut-count">{customLooks.length}</span>
				{/if}
			</button>
			<button type="button" class="upload-btn" onclick={() => fileInput?.click()} disabled={uploading} aria-label="Upload .cube">
				{@html renderIcon(ICON_UPLOAD, 11)}
			</button>
		</div>
		<input bind:this={fileInput} type="file" accept=".cube" onchange={handleUpload} style="display:none" />
		{#if lutOpen && customLooks.length > 0}
			<div class="custom-list">
				{#each customLooks as look}
					<div class="custom-item">
						<span class="custom-name">{formatLookName(look.name)}</span>
						<button type="button" class="del-btn" onclick={() => handleDelete(look.name)} title="Delete" aria-label="Delete {look.name}">
							{@html renderIcon(ICON_CLOSE, 10)}
						</button>
					</div>
				{/each}
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
		font-family: var(--font-ui, sans-serif);
	}

	.top-bar {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 4px 8px;
		flex-shrink: 0;
		border-bottom: 1px solid var(--border-subtle);
	}

	.group-bar {
		display: flex;
		align-items: center;
		gap: 3px;
		overflow-x: auto;
		flex-shrink: 0;
	}

	.group-bar::-webkit-scrollbar { height: 0; }

	.pill {
		padding: 3px 9px;
		background: transparent;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: var(--text-xs);
		font-weight: 500;
		white-space: nowrap;
		transition: all var(--transition-fast);
		flex-shrink: 0;
	}

	.pill:hover { color: var(--text-secondary); border-color: var(--border-default); }

	.pill-active {
		background: color-mix(in srgb, var(--accent-yellow) 12%, transparent);
		border-color: var(--accent-yellow);
		color: var(--accent-yellow);
	}

	.pill-off { color: var(--text-tertiary); border-color: var(--border-default); }
	.pill-off:hover { color: var(--text-primary); }

	.intensity-ctrl {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-left: auto;
		flex-shrink: 0;
	}

	.active-name {
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--accent-yellow);
		max-width: 110px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.intensity-slider {
		width: 60px;
		height: 3px;
		accent-color: var(--accent-yellow);
	}

	.intensity-val {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--text-secondary);
		width: 2.5rem;
		text-align: right;
	}

	.looks-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		overflow-x: hidden;
		padding: 6px 8px;
	}

	.looks-scroll::-webkit-scrollbar { width: 3px; }
	.looks-scroll::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	.looks-grid {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
		align-content: start;
	}

	.look-chip {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px 4px 5px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		cursor: pointer;
		font-family: var(--font-ui);
		transition: all var(--transition-fast);
		position: relative;
	}

	.look-chip:hover {
		background: var(--bg-hover);
		border-color: var(--border-default);
	}

	.look-chip.active {
		border-color: var(--look-accent);
		box-shadow: 0 0 0 1px color-mix(in srgb, var(--look-accent) 30%, transparent);
		background: color-mix(in srgb, var(--look-accent) 8%, var(--bg-elevated));
	}

	.look-swatch {
		width: 16px;
		height: 16px;
		border-radius: 3px;
		background: linear-gradient(135deg, var(--look-accent), color-mix(in srgb, var(--look-accent) 40%, #111));
		flex-shrink: 0;
		border: 1px solid color-mix(in srgb, var(--look-accent) 25%, transparent);
	}

	.look-chip.active .look-swatch {
		border-color: var(--look-accent);
		box-shadow: 0 0 4px color-mix(in srgb, var(--look-accent) 40%, transparent);
	}

	.look-name {
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-primary);
		white-space: nowrap;
		line-height: 1.2;
	}

	.look-chip.active .look-name {
		color: var(--look-accent);
		font-weight: 600;
	}

	.look-check {
		display: flex;
		color: var(--look-accent);
		margin-left: -2px;
	}

	.no-looks {
		color: var(--text-tertiary);
		font-size: var(--text-xs);
		font-style: italic;
		padding: 4px;
	}

	.lut-section {
		flex-shrink: 0;
		border-top: 1px solid var(--border-subtle);
	}

	.lut-row {
		display: flex;
		align-items: center;
		padding: 3px 8px;
		gap: 0;
	}

	.lut-toggle {
		display: flex;
		align-items: center;
		gap: 5px;
		flex: 1;
		padding: 0;
		background: transparent;
		border: none;
		cursor: pointer;
		font-family: var(--font-ui);
	}

	.lut-row:hover { background: var(--bg-hover); }

	.lut-chevron {
		display: flex;
		color: var(--text-tertiary);
		flex-shrink: 0;
	}

	.lut-label {
		font-size: var(--text-2xs);
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--text-tertiary);
	}

	.lut-count {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--text-tertiary);
		background: var(--bg-control);
		padding: 0 3px;
		border-radius: var(--radius-xs);
	}

	.upload-btn {
		display: flex;
		align-items: center;
		margin-left: auto;
		padding: 2px 4px;
		background: transparent;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		cursor: pointer;
		transition: all var(--transition-fast);
	}

	.upload-btn:hover { color: var(--text-primary); border-color: var(--border-default); }

	.custom-list {
		display: flex;
		gap: 3px;
		flex-wrap: wrap;
		padding: 3px 8px 4px;
	}

	.custom-item {
		display: flex;
		align-items: center;
		gap: 3px;
		padding: 1px 5px;
		background: var(--bg-control);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
	}

	.custom-name {
		font-size: var(--text-2xs);
		color: var(--text-secondary);
	}

	.del-btn {
		display: flex;
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		padding: 0;
		transition: color var(--transition-fast);
	}

	.del-btn:hover { color: var(--color-error); }
</style>

<script lang="ts">
	import type { PlayoutClip } from '$lib/api/types';
	import { playoutClips } from '$lib/api/switch-api';

	interface Props {
		onSelect: (clip: { name: string; path: string; duration: number }) => void;
	}

	let { onSelect }: Props = $props();

	let clips = $state<PlayoutClip[]>([]);
	let loading = $state(false);
	let error = $state('');

	const uploaded = $derived(clips.filter(c => c.source === 'clip-store' || c.source === 'recording'));
	const samples = $derived(clips.filter(c => c.source === 'sample'));

	async function fetchClips() {
		loading = true;
		error = '';
		try {
			clips = await playoutClips();
		} catch (e) {
			error = 'Failed to load clips';
			clips = [];
		} finally {
			loading = false;
		}
	}

	function formatDuration(sec: number): string {
		const m = Math.floor(sec / 60);
		const s = sec % 60;
		return `${m}:${String(s).padStart(2, '0')}`;
	}

	// Fetch on mount.
	$effect(() => {
		fetchClips();
	});
</script>

<div class="clip-browser" data-testid="playout-clip-browser">
	<div class="browser-header">
		<span class="browser-title">CLIP LIBRARY</span>
		<button
			class="refresh-btn"
			onclick={fetchClips}
			disabled={loading}
			title="Refresh clip list"
		>&#x21bb;</button>
	</div>

	{#if loading && clips.length === 0}
		<div class="browser-empty">Loading...</div>
	{:else if error && clips.length === 0}
		<div class="browser-empty">{error}</div>
	{:else if clips.length === 0}
		<div class="browser-empty">No clips available</div>
	{:else}
		<div class="clip-list">
			{#if uploaded.length > 0}
				<div class="group-header">Uploaded</div>
				{#each uploaded as clip}
					<button
						class="clip-row"
						onclick={() => onSelect({ name: clip.name, path: clip.path, duration: clip.duration })}
						title="Insert {clip.name} into playlist"
					>
						<span class="clip-name">{clip.name}</span>
						<span class="clip-dur">{formatDuration(clip.duration)}</span>
					</button>
				{/each}
			{/if}
			{#if samples.length > 0}
				<div class="group-header">Samples</div>
				{#each samples as clip}
					<button
						class="clip-row"
						onclick={() => onSelect({ name: clip.name, path: clip.path, duration: clip.duration })}
						title="Insert {clip.name} into playlist"
					>
						<span class="clip-name">{clip.name}</span>
						<span class="clip-dur">{formatDuration(clip.duration)}</span>
					</button>
				{/each}
			{/if}
		</div>
	{/if}
</div>

<style>
	.clip-browser {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		overflow: hidden;
		flex-shrink: 0;
	}

	.browser-header {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 5px 8px;
		background: var(--bg-control);
		border-bottom: 1px solid var(--border-subtle);
	}

	.browser-title {
		flex: 1;
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.refresh-btn {
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
		font-size: 12px;
		transition: color var(--transition-fast), border-color var(--transition-fast);
	}

	.refresh-btn:hover {
		color: var(--text-secondary);
		border-color: var(--text-secondary);
	}

	.refresh-btn:disabled {
		opacity: 0.5;
		cursor: default;
	}

	.browser-empty {
		padding: 8px;
		font-family: var(--font-ui);
		font-size: 9px;
		color: var(--text-tertiary);
		text-align: center;
	}

	.clip-list {
		max-height: 160px;
		overflow-y: auto;
	}

	.clip-list::-webkit-scrollbar { width: 3px; }
	.clip-list::-webkit-scrollbar-thumb { background: var(--border-subtle); border-radius: 2px; }

	.group-header {
		padding: 3px 8px 1px;
		font-family: var(--font-ui);
		font-size: 8px;
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		background: var(--bg-base);
		border-top: 1px solid var(--border-subtle);
	}

	.group-header:first-child {
		border-top: none;
	}

	.clip-row {
		display: flex;
		align-items: center;
		width: 100%;
		padding: 3px 8px;
		border: none;
		background: transparent;
		cursor: pointer;
		text-align: left;
		transition: background var(--transition-fast);
	}

	.clip-row:hover {
		background: var(--bg-hover);
	}

	.clip-name {
		flex: 1;
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-secondary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.clip-dur {
		flex-shrink: 0;
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
		margin-left: 6px;
	}
</style>

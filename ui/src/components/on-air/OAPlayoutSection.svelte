<script lang="ts">
	import type { PlayoutChannelState, PlaylistItem } from '$lib/api/types';
	import OASchedulePanel from './OASchedulePanel.svelte';

	interface Props {
		enabled: boolean;
		onToggle: (enabled: boolean) => void;
		playoutChannel?: PlayoutChannelState;
		playlistItems?: PlaylistItem[];
		onNewSchedule?: () => void;
		onLoadFile?: (file: File) => void;
		onLoadSaved?: (name: string) => void;
		savedPlaylists?: string[];
		onShowAsRun?: () => void;
	}

	let {
		enabled,
		onToggle,
		playoutChannel,
		playlistItems = [],
		onNewSchedule,
		onLoadFile,
		onLoadSaved,
		savedPlaylists = [],
		onShowAsRun,
	}: Props = $props();

	let fileInput: HTMLInputElement | undefined = $state();

	function handleFileChange(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		if (input.files && input.files.length > 0 && onLoadFile) {
			onLoadFile(input.files[0]);
			input.value = '';
		}
	}

	function handleLoadSaved(e: Event) {
		const select = e.currentTarget as HTMLSelectElement;
		if (select.value && onLoadSaved) {
			onLoadSaved(select.value);
			select.value = '';
		}
	}
</script>

<div class="playout-section">
	<div class="playout-header">
		<button
			class="toggle-btn"
			class:toggle-on={enabled}
			onclick={() => onToggle(!enabled)}
		>
			{enabled ? 'ON' : 'OFF'}
		</button>
	</div>

	{#if !enabled}
		<p class="disabled-msg">Playout not enabled — enable to create or load a schedule</p>
	{:else}
		<div class="playout-controls">
			<div class="button-row">
				{#if onNewSchedule}
					<button class="ctrl-btn" onclick={onNewSchedule}>New Schedule</button>
				{/if}
				{#if onLoadFile}
					<button class="ctrl-btn" onclick={() => fileInput?.click()}>Load File</button>
					<input
						bind:this={fileInput}
						type="file"
						accept=".json,.xml,.bxf"
						style="display: none"
						onchange={handleFileChange}
					/>
				{/if}
				{#if onLoadSaved && savedPlaylists.length > 0}
					<select class="load-select" onchange={handleLoadSaved} role="combobox">
						<option value="">Load Saved...</option>
						{#each savedPlaylists as name}
							<option value={name}>{name}</option>
						{/each}
					</select>
				{/if}
			</div>

			{#if playoutChannel}
				<div class="schedule-container">
					<OASchedulePanel
						{playoutChannel}
						items={playlistItems}
						onShowAsRun={onShowAsRun}
					/>
				</div>
			{/if}
		</div>
	{/if}
</div>

<style>
	.playout-section {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.playout-header {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.toggle-btn {
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 700;
		cursor: pointer;
		background: var(--bg-control);
		color: var(--text-secondary);
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast),
			color var(--transition-fast);
	}

	.toggle-on {
		background: rgba(34, 197, 94, 0.15);
		border-color: var(--accent-green, #22c55e);
		color: var(--accent-green, #22c55e);
	}

	.disabled-msg {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		color: var(--text-secondary);
		margin: 0;
	}

	.playout-controls {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}

	.button-row {
		display: flex;
		gap: 6px;
		flex-wrap: wrap;
	}

	.ctrl-btn {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 600;
		color: var(--text-primary);
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 4px 10px;
		cursor: pointer;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast);
	}

	.ctrl-btn:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	.load-select {
		font-family: var(--font-ui);
		font-size: 11px;
		font-weight: 600;
		color: var(--text-primary);
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 4px 8px;
		cursor: pointer;
	}

	.schedule-container {
		min-height: 100px;
	}
</style>

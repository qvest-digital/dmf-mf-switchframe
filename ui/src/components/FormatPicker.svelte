<script lang="ts">
	import { getFormat, setFormat, apiCall } from '$lib/api/switch-api';
	import ConfirmDialog from './ConfirmDialog.svelte';

	interface Props {
		badgeText: string;
		isDualEngine?: boolean;
	}
	let { badgeText, isDualEngine = false }: Props = $props();

	let dropdownOpen = $state(false);
	let presets = $state<string[]>([]);
	let currentFormatName = $state('');
	let pendingFormat = $state<string | null>(null);
	let loading = $state(false);
	let changing = $state(false);

	const confirmMessage = $derived(
		isDualEngine
			? 'Both engines will restart. Program output will be interrupted for ~5 seconds.'
			: 'Changing format will restart the engine. Program output will be interrupted for ~5 seconds.'
	);

	async function openDropdown() {
		if (dropdownOpen) {
			dropdownOpen = false;
			return;
		}
		loading = true;
		try {
			const result = await getFormat();
			presets = result.presets.sort();
			currentFormatName = result.format.name;
			dropdownOpen = true;
		} finally {
			loading = false;
		}
	}

	function selectFormat(name: string) {
		if (name === currentFormatName) {
			// Same format, just close
			dropdownOpen = false;
			return;
		}
		pendingFormat = name;
	}

	async function confirmChange() {
		if (!pendingFormat || changing) return;
		changing = true;
		try {
			await apiCall(setFormat(pendingFormat), 'Format change failed');
		} finally {
			changing = false;
		}
		pendingFormat = null;
		dropdownOpen = false;
	}

	function cancelChange() {
		pendingFormat = null;
		dropdownOpen = false;
	}

	function handleClickOutside(e: MouseEvent) {
		if (dropdownOpen && !pendingFormat) {
			const target = e.target as HTMLElement;
			if (!target.closest('.format-picker')) {
				dropdownOpen = false;
			}
		}
	}
</script>

<svelte:document onmousedown={handleClickOutside} />

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="format-picker" onkeydown={() => {}}>
	<button
		class="format-badge"
		onclick={openDropdown}
		title="Change pipeline format"
		disabled={loading || changing}
	>
		{badgeText}
	</button>

	{#if dropdownOpen}
		<div class="format-dropdown">
			{#each presets as preset}
				<button
					class="format-option"
					class:current={preset === currentFormatName}
					onclick={() => selectFormat(preset)}
				>
					{preset}
					{#if preset === currentFormatName}
						<span class="current-indicator">current</span>
					{/if}
				</button>
			{/each}
		</div>
	{/if}
</div>

<ConfirmDialog
	open={pendingFormat !== null}
	title="Change Format"
	message={confirmMessage}
	confirmLabel="Change Format"
	onconfirm={confirmChange}
	oncancel={cancelChange}
/>

<style>
	.format-picker {
		position: relative;
	}

	.format-badge {
		padding: 2px 8px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-elevated);
		color: var(--text-muted, var(--text-secondary));
		font-family: var(--font-mono, var(--font-ui));
		font-size: var(--text-xs);
		letter-spacing: 0.03em;
		white-space: nowrap;
		opacity: 0.8;
		cursor: pointer;
		transition:
			border-color var(--transition-fast),
			opacity var(--transition-fast);
	}

	.format-badge:hover {
		opacity: 1;
		border-color: var(--border-strong);
	}

	.format-badge:disabled {
		cursor: wait;
	}

	.format-dropdown {
		position: absolute;
		top: calc(100% + 4px);
		left: 0;
		min-width: 160px;
		max-height: 320px;
		overflow-y: auto;
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
		z-index: var(--z-popover, 100);
		display: flex;
		flex-direction: column;
		padding: 4px;
	}

	.format-option {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 6px 10px;
		border: none;
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--text-primary);
		font-family: var(--font-mono, var(--font-ui));
		font-size: var(--text-sm);
		cursor: pointer;
		text-align: left;
		white-space: nowrap;
		transition: background var(--transition-fast);
	}

	.format-option:hover {
		background: var(--bg-hover);
	}

	.format-option.current {
		color: var(--text-secondary);
	}

	.current-indicator {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		color: var(--text-dim);
		opacity: 0.6;
	}
</style>

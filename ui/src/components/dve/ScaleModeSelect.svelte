<script lang="ts">
	interface Props {
		value: string;
		onChange: (mode: string) => void;
	}

	let { value, onChange }: Props = $props();

	const modes = [
		{ id: 'stretch', label: 'Stretch' },
		{ id: 'fill', label: 'Fill' },
		{ id: 'fit', label: 'Fit' },
	] as const;
</script>

<div class="scale-mode-select" role="radiogroup" aria-label="Scale mode">
	{#each modes as mode (mode.id)}
		<button
			type="button"
			class="mode-btn"
			class:active={value === mode.id}
			title={mode.label}
			tabindex={value === mode.id ? 0 : -1}
			role="radio"
			aria-checked={value === mode.id}
			onclick={() => onChange(mode.id)}
		>
			<svg width="16" height="12" viewBox="0 0 16 12" fill="none">
				{#if mode.id === 'stretch'}
					<!-- Stretched rectangle with bi-directional arrows -->
					<rect x="1" y="2" width="14" height="8" rx="0.5"
						stroke="currentColor" stroke-width="1" fill="none" />
					<line x1="3" y1="6" x2="6" y2="6" stroke="currentColor" stroke-width="0.8" />
					<polyline points="3.5,4.5 3,6 3.5,7.5" stroke="currentColor" stroke-width="0.8" fill="none" />
					<line x1="10" y1="6" x2="13" y2="6" stroke="currentColor" stroke-width="0.8" />
					<polyline points="12.5,4.5 13,6 12.5,7.5" stroke="currentColor" stroke-width="0.8" fill="none" />
					<line x1="8" y1="3" x2="8" y2="5" stroke="currentColor" stroke-width="0.8" />
					<polyline points="6.8,3.5 8,3 9.2,3.5" stroke="currentColor" stroke-width="0.8" fill="none" />
					<line x1="8" y1="7" x2="8" y2="9" stroke="currentColor" stroke-width="0.8" />
					<polyline points="6.8,8.5 8,9 9.2,8.5" stroke="currentColor" stroke-width="0.8" fill="none" />
				{:else if mode.id === 'fill'}
					<!-- Rectangle with content overflowing -->
					<rect x="1" y="2" width="14" height="8" rx="0.5"
						stroke="currentColor" stroke-width="1" fill="none" />
					<rect x="3" y="0.5" width="10" height="11" rx="0.3"
						stroke="currentColor" stroke-width="0.6" stroke-dasharray="1.5 1" fill="none" opacity="0.6" />
					<line x1="1" y1="2" x2="3" y2="0.5" stroke="currentColor" stroke-width="0.4" opacity="0.4" />
					<line x1="15" y1="2" x2="13" y2="0.5" stroke="currentColor" stroke-width="0.4" opacity="0.4" />
				{:else}
					<!-- Rectangle with letterbox bars -->
					<rect x="1" y="2" width="14" height="8" rx="0.5"
						stroke="currentColor" stroke-width="1" fill="none" />
					<rect x="1" y="2" width="2" height="8" fill="currentColor" opacity="0.25" />
					<rect x="13" y="2" width="2" height="8" fill="currentColor" opacity="0.25" />
					<rect x="3" y="2" width="10" height="8" rx="0.3"
						stroke="currentColor" stroke-width="0.5" stroke-dasharray="1 1" fill="none" opacity="0.5" />
				{/if}
			</svg>
		</button>
	{/each}
</div>

<style>
	.scale-mode-select {
		display: inline-flex;
		gap: 1px;
		background: var(--border-subtle, #2a2a2e);
		border-radius: 3px;
		overflow: hidden;
	}

	.mode-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 24px;
		height: 20px;
		padding: 0;
		border: none;
		background: var(--bg-control, #1e1e22);
		color: var(--text-tertiary, #666);
		cursor: pointer;
		transition: color 0.12s, background 0.12s;
	}

	.mode-btn:hover {
		color: var(--text-secondary, #999);
		background: rgba(255, 255, 255, 0.04);
	}

	.mode-btn.active {
		color: var(--accent-gold, #d4a017);
		background: rgba(212, 160, 23, 0.08);
	}

	.mode-btn:focus-visible {
		outline: 1px solid var(--accent-gold, #d4a017);
		outline-offset: -1px;
		z-index: 1;
	}

	.mode-btn svg {
		display: block;
	}
</style>

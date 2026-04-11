<script lang="ts">
	interface SlateInfo {
		clipId: string;
		name: string;
		isOnAir?: boolean;
	}

	interface Props {
		slates: SlateInfo[];
		onFireSlate: (clipId: string, name: string) => void;
		onAddSlate: () => void;
		onRemoveSlate: (clipId: string) => void;
	}

	let { slates, onFireSlate, onAddSlate, onRemoveSlate }: Props = $props();
</script>

<div class="slates-grid">
	{#each slates as slate (slate.clipId)}
		<button
			class="slate-btn"
			class:on-air={slate.isOnAir}
			onclick={() => onFireSlate(slate.clipId, slate.name)}
			oncontextmenu={(e) => { e.preventDefault(); onRemoveSlate(slate.clipId); }}
		>
			<span class="slate-name">{slate.name}</span>
			{#if slate.isOnAir}
				<span class="on-air-badge">ON AIR</span>
			{/if}
		</button>
	{/each}
	<button class="slate-btn add-btn" onclick={onAddSlate}>
		<span class="add-icon">+</span>
	</button>
</div>

<style>
	.slates-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
		gap: 6px;
	}

	.slate-btn {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 4px;
		background: var(--bg-elevated);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		padding: 8px 12px;
		cursor: pointer;
		transition:
			background var(--transition-fast),
			border-color var(--transition-fast);
	}

	.slate-btn:hover {
		background: var(--bg-hover);
		border-color: var(--border-strong);
	}

	.slate-name {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		color: var(--text-primary);
		text-align: center;
		word-break: break-word;
	}

	.slate-btn.on-air {
		border-color: var(--tally-program);
		background: rgba(220, 38, 38, 0.1);
	}

	.on-air-badge {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--tally-program);
		text-transform: uppercase;
	}

	.add-btn {
		border-style: dashed;
		border-color: var(--border-strong);
		background: transparent;
	}

	.add-btn:hover {
		background: var(--bg-elevated);
	}

	.add-icon {
		font-size: 18px;
		font-weight: 400;
		color: var(--text-secondary);
		line-height: 1;
	}
</style>

<script lang="ts">
	import type { DVESlotState } from '$lib/api/types';

	interface Props {
		dve: DVESlotState;
		expanded: boolean;
		onToggle: () => void;
		onReset: () => void;
		onInputChange: (field: string, e: Event, step: number, min?: number, max?: number) => void;
		onDragStart: (
			e: PointerEvent,
			field: string,
			currentValue: number,
			step: number,
			min?: number,
			max?: number,
		) => void;
	}

	let { dve, expanded, onToggle, onReset, onInputChange, onDragStart }: Props = $props();

	function fmtNum(v: number | undefined, fallback: number): string {
		return (v ?? fallback).toFixed(1);
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="section-header" onclick={onToggle}>
	<span class="section-arrow">{expanded ? '\u25BC' : '\u25B6'}</span>
	<span class="section-title">Effects</span>
	<button
		class="reset-btn"
		type="button"
		onclick={(e) => {
			e.stopPropagation();
			onReset();
		}}
		title="Reset effects"
	>
		&#x27F2;
	</button>
</div>
{#if expanded}
	<div class="section-content">
		<label class="field-label">Defocus</label>
		<input
			type="number"
			class="dve-input"
			value={fmtNum(dve.defocusRadius, 0)}
			onchange={(e) => onInputChange('defocusRadius', e, 0.5, 0, 50)}
			onpointerdown={(e) => onDragStart(e, 'defocusRadius', dve.defocusRadius ?? 0, 0.5, 0, 50)}
		/>
	</div>
{/if}

<style>
	.section-header {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 4px 8px;
		background: var(--bg-surface, #0f0f12);
		border: none;
		border-bottom: 1px solid var(--border-subtle);
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: var(--text-2xs, 0.5rem);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		width: 100%;
	}

	.section-header:hover {
		background: var(--bg-hover, #2c2c32);
	}

	.section-arrow {
		font-size: 8px;
		width: 10px;
		text-align: center;
		color: var(--text-tertiary);
	}

	.section-title {
		flex: 1;
		text-align: left;
	}

	.reset-btn {
		padding: 0 3px;
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		font-size: 11px;
		line-height: 1;
		border-radius: var(--radius-xs, 3px);
	}

	.reset-btn:hover {
		color: var(--text-primary);
		background: var(--bg-hover, #2c2c32);
	}

	.section-content {
		padding: 6px 8px;
		display: grid;
		grid-template-columns: auto 1fr;
		gap: 3px 8px;
		align-items: center;
	}

	.field-label {
		font-size: var(--text-2xs, 0.5rem);
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.04em;
		text-align: right;
		padding-right: 2px;
		user-select: none;
	}

	.dve-input {
		width: 64px;
		padding: 2px 4px;
		background: var(--bg-control, #242429);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-xs, 3px);
		color: var(--text-primary);
		font-family: var(--font-mono);
		font-size: var(--text-2xs, 0.5rem);
		text-align: right;
		cursor: ew-resize;
		appearance: textfield;
		-moz-appearance: textfield;
	}

	.dve-input::-webkit-inner-spin-button,
	.dve-input::-webkit-outer-spin-button {
		-webkit-appearance: none;
		margin: 0;
	}

	.dve-input:focus {
		cursor: text;
		border-color: var(--border-strong, #555);
		outline: none;
	}
</style>

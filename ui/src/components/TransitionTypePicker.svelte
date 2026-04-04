<script lang="ts">
	import type { WipeConfig } from '$lib/api/types';
	import WipePatternBrowser from './WipePatternBrowser.svelte';

	interface Props {
		selectedType: string;
		wipeConfig?: WipeConfig;
		onTypeChange: (type: string) => void;
		onWipeConfigChange?: (config: WipeConfig) => void;
	}

	let { selectedType, wipeConfig, onTypeChange, onWipeConfigChange }: Props = $props();

	const categories: Record<string, { value: string; label: string }[]> = {
		'Standard': [
			{ value: 'mix', label: 'Mix' },
			{ value: 'dip', label: 'Dip' },
			{ value: 'wipe', label: 'Wipe' },
			{ value: 'stinger', label: 'Stinger' },
			{ value: 'nam', label: 'NAM' },
		],
		'DVE 2D': [
			{ value: 'dve-push-left', label: 'Push L' },
			{ value: 'dve-push-right', label: 'Push R' },
			{ value: 'dve-push-up', label: 'Push Up' },
			{ value: 'dve-push-down', label: 'Push Dn' },
			{ value: 'dve-squeeze', label: 'Squeeze' },
			{ value: 'dve-spin', label: 'Spin' },
			{ value: 'dve-zoom', label: 'Zoom' },
			{ value: 'dve-fly', label: 'Fly' },
			{ value: 'dve-swing-left', label: 'Swing L' },
			{ value: 'dve-swing-right', label: 'Swing R' },
			{ value: 'dve-reveal-left', label: 'Reveal L' },
			{ value: 'dve-reveal-right', label: 'Reveal R' },
			{ value: 'dve-reveal-up', label: 'Reveal Up' },
			{ value: 'dve-reveal-down', label: 'Reveal Dn' },
			{ value: 'dve-scale-rotate', label: 'Rotate' },
			{ value: 'dve-cross-zoom', label: 'X-Zoom' },
			{ value: 'dve-shrink-grow', label: 'PIP Swap' },
			{ value: 'dve-barn-h', label: 'Barn H' },
			{ value: 'dve-barn-v', label: 'Barn V' },
			{ value: 'dve-mosaic', label: 'Mosaic' },
			{ value: 'dve-corner-peel', label: 'Peel' },
			{ value: 'dve-bounce', label: 'Bounce' },
			{ value: 'dve-blur-dissolve', label: 'Blur Diss' },
		],
		'DVE 3D': [
			{ value: 'dve-cube', label: 'Cube' },
			{ value: 'dve-flip', label: 'Flip' },
			{ value: 'dve-door', label: 'Door' },
			{ value: 'dve-carousel', label: 'Carousel' },
			{ value: 'dve-tumble', label: 'Tumble' },
			{ value: 'dve-swing-door', label: 'Swing' },
			{ value: 'dve-fold', label: 'Fold' },
			{ value: 'dve-cube-roll', label: 'Cube Roll' },
			{ value: 'dve-globe', label: 'Globe' },
			{ value: 'dve-page-turn', label: 'Page Turn' },
		],
	};

	let expandedCategories = $state<Record<string, boolean>>({ 'Standard': true });

	function toggleCategory(cat: string) {
		expandedCategories = { ...expandedCategories, [cat]: !expandedCategories[cat] };
	}

	// Determine which category the selected type belongs to
	const selectedCategory = $derived(() => {
		for (const [cat, items] of Object.entries(categories)) {
			if (items.some(i => i.value === selectedType)) return cat;
		}
		return 'Standard';
	});

	// Auto-expand the category containing the selected type
	$effect(() => {
		const cat = selectedCategory();
		if (cat && !expandedCategories[cat]) {
			expandedCategories = { ...expandedCategories, [cat]: true };
		}
	});
</script>

<div class="type-picker">
	{#each Object.entries(categories) as [catName, items]}
		<div class="picker-category">
			<button class="cat-header" onclick={() => toggleCategory(catName)}>
				<span class="cat-arrow">{expandedCategories[catName] ? '\u25BE' : '\u25B8'}</span>
				<span class="cat-name">{catName}</span>
			</button>
			{#if expandedCategories[catName]}
				<div class="cat-items">
					{#each items as item}
						<button
							class="type-btn"
							class:selected={selectedType === item.value}
							onclick={() => onTypeChange(item.value)}
							title={item.value}
						>{item.label}</button>
					{/each}
				</div>
			{/if}
		</div>
	{/each}

	{#if selectedType === 'wipe' && wipeConfig && onWipeConfigChange}
		<div class="wipe-section">
			<WipePatternBrowser config={wipeConfig} onConfigChange={onWipeConfigChange} />
		</div>
	{/if}
</div>

<style>
	.type-picker {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.picker-category {
		display: flex;
		flex-direction: column;
	}

	.cat-header {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 3px 4px;
		border: none;
		background: transparent;
		color: var(--text-secondary);
		cursor: pointer;
		font-family: var(--font-ui);
		font-size: 10px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		transition: color var(--transition-fast);
	}

	.cat-header:hover {
		color: var(--text-primary);
	}

	.cat-arrow {
		font-size: 8px;
		width: 8px;
		color: var(--text-tertiary);
	}

	.cat-name {
		flex: 1;
		text-align: left;
	}

	.cat-items {
		display: flex;
		flex-wrap: wrap;
		gap: 2px;
		padding: 0 4px 4px 16px;
	}

	.type-btn {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 500;
		padding: 2px 6px;
		border: 1px solid var(--border-subtle);
		border-radius: 3px;
		background: var(--bg-base);
		color: var(--text-tertiary);
		cursor: pointer;
		white-space: nowrap;
		transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
	}

	.type-btn:hover {
		border-color: var(--border-default);
		color: var(--text-secondary);
	}

	.type-btn.selected {
		border-color: var(--accent-yellow);
		background: var(--accent-yellow-dim);
		color: var(--accent-yellow);
	}

	.wipe-section {
		padding: 4px 4px 0 16px;
		border-top: 1px solid var(--border-subtle);
		margin-top: 2px;
	}
</style>

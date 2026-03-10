<script lang="ts">
	import { onMount } from 'svelte';

	interface Props {
		children: import('svelte').Snippet<[string]>;
		onTabChange?: (tab: string) => void;
	}
	let { children, onTabChange }: Props = $props();

	const tabs = ['Audio', 'Graphics', 'Macros', 'Keys', 'Replay', 'Presets', 'SCTE-35', 'Layout'] as const;
	type TabId = typeof tabs[number];

	function loadSavedTab(): TabId {
		if (typeof localStorage === 'undefined') return 'Audio';
		const saved = localStorage.getItem('sf-active-tab');
		if (saved && (tabs as readonly string[]).includes(saved)) return saved as TabId;
		return 'Audio';
	}

	let activeTab = $state<TabId>(loadSavedTab());

	function setTab(tab: TabId) {
		activeTab = tab;
		localStorage.setItem('sf-active-tab', tab);
		onTabChange?.(tab);
	}

	// Keyboard shortcut: Ctrl+Shift+1-6
	function handleKeydown(e: KeyboardEvent) {
		if (e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey) {
			const match = e.code.match(/^Digit([1-8])$/);
			if (match) {
				e.preventDefault();
				e.stopPropagation();
				setTab(tabs[parseInt(match[1]) - 1]);
			}
		}
	}

	onMount(() => {
		document.addEventListener('keydown', handleKeydown, true);
		return () => document.removeEventListener('keydown', handleKeydown, true);
	});
</script>

<div class="bottom-tabs">
	<div class="tab-bar" role="tablist" aria-label="Bottom panel">
		{#each tabs as tab, i}
			<button
				id="tab-{tab.toLowerCase()}"
				class="tab"
				class:active={activeTab === tab}
				role="tab"
				aria-selected={activeTab === tab}
				aria-controls="tabpanel-{tab.toLowerCase()}"
				onclick={() => setTab(tab)}
			>
				{tab}
				<span class="tab-shortcut">^{i + 1}</span>
			</button>
		{/each}
	</div>
	<div
		class="tab-content"
		role="tabpanel"
		id="tabpanel-{activeTab.toLowerCase()}"
		aria-labelledby="tab-{activeTab.toLowerCase()}"
	>
		{@render children?.(activeTab)}
	</div>
</div>

<style>
	.bottom-tabs {
		display: flex;
		flex-direction: column;
		height: 100%;
	}

	.tab-bar {
		display: flex;
		gap: 0;
		background: var(--bg-base);
		border-bottom: 1px solid var(--border-default);
		flex-shrink: 0;
		height: 25px;
	}

	.tab {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-tertiary);
		background: transparent;
		border: none;
		border-bottom: 2px solid transparent;
		padding: 0 12px;
		cursor: pointer;
		transition: color var(--transition-fast), border-color var(--transition-fast), background var(--transition-fast);
		display: flex;
		align-items: center;
		gap: 4px;
	}

	.tab:hover {
		color: var(--text-secondary);
		background: rgba(255, 255, 255, 0.02);
	}

	.tab.active {
		color: var(--text-primary);
		border-bottom-color: var(--accent-yellow);
		background: rgba(255, 255, 255, 0.02);
	}

	.tab-shortcut {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		opacity: 0.2;
	}

	.tab-content {
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}
</style>

<script lang="ts">
	import { onMount } from 'svelte';

	interface Props {
		children: import('svelte').Snippet<[string]>;
		onTabChange?: (tab: string) => void;
		replayActive?: boolean;
		aiSegmentAvailable?: boolean;
	}
	let { children, onTabChange, replayActive = false, aiSegmentAvailable = false }: Props = $props();

	const staticTabs = ['Audio', 'Layout', 'Graphics', 'Replay', 'Keys', 'Captions', 'SCTE', 'Macros', 'Presets', 'Clips', 'Team', 'STMap'] as const;
	type StaticTabId = typeof staticTabs[number];
	type TabId = StaticTabId | 'AI BG';

	let tabs = $derived(
		aiSegmentAvailable
			? ([...staticTabs, 'AI BG'] as TabId[])
			: ([...staticTabs] as TabId[]),
	);

	function loadSavedTab(): TabId {
		if (typeof localStorage === 'undefined') return 'Audio';
		const saved = localStorage.getItem('sf-active-tab');
		// Accept any known tab name — 'AI BG' is valid if server reports available
		if (saved) return saved as TabId;
		return 'Audio';
	}

	let activeTab = $state<TabId>(loadSavedTab());

	function setTab(tab: TabId) {
		activeTab = tab;
		localStorage.setItem('sf-active-tab', tab);
		onTabChange?.(tab);
	}

	// Keyboard shortcut: Ctrl+Shift+1-9,0
	function handleKeydown(e: KeyboardEvent) {
		if (e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey) {
			const match = e.code.match(/^Digit([0-9])$/);
			if (match) {
				const digit = parseInt(match[1]);
				const idx = digit === 0 ? 9 : digit - 1; // 0 maps to 10th tab
				if (idx < tabs.length) {
					e.preventDefault();
					e.stopPropagation();
					setTab(tabs[idx]);
				}
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
				id="tab-{tab.toLowerCase().replace(' ', '-')}"
				class="tab"
				class:active={activeTab === tab}
				class:tab--ai={tab === 'AI BG'}
				role="tab"
				aria-selected={activeTab === tab}
				aria-controls="tabpanel-{tab.toLowerCase().replace(' ', '-')}"
				onclick={() => setTab(tab)}
			>
				{tab}
				{#if tab === 'Replay' && replayActive}
					<span class="replay-dot"></span>
				{/if}
				{#if tab === 'AI BG'}
					<span class="ai-dot"></span>
				{/if}
				<span class="tab-shortcut">^{(i + 1) % 10}</span>
			</button>
		{/each}
	</div>
	<div
		class="tab-content"
		role="tabpanel"
		id="tabpanel-{activeTab.toLowerCase().replace(' ', '-')}"
		aria-labelledby="tab-{activeTab.toLowerCase().replace(' ', '-')}"
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

	.replay-dot {
		display: inline-block;
		width: 6px;
		height: 6px;
		background: var(--accent-orange);
		border-radius: 50%;
		margin-left: 2px;
		animation: pulse-dot 1.5s ease-in-out infinite;
	}

	@keyframes pulse-dot {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.3; }
	}

	.tab-shortcut {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		opacity: 0.2;
	}

	.tab--ai.active {
		border-bottom-color: #a855f7;
	}

	.ai-dot {
		display: inline-block;
		width: 5px;
		height: 5px;
		background: #a855f7;
		border-radius: 50%;
		margin-left: 1px;
		opacity: 0.7;
	}

	.tab-content {
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}
</style>

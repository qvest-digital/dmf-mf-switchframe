<script lang="ts">
	import type { ControlRoomState } from '$lib/api/types';

	interface Props {
		visible: boolean;
		onclose: () => void;
		crState: ControlRoomState;
	}

	let { visible, onclose, crState }: Props = $props();

	let activeTab = $state<string>('presets');

	const tabs = ['presets', 'dve', 'keys', 'video', 'encoder', 'format', 'team'] as const;

	const tabLabels: Record<typeof tabs[number], string> = {
		presets: 'Presets',
		dve: 'DVE',
		keys: 'Keys',
		video: 'Video',
		encoder: 'Encoder',
		format: 'Format',
		team: 'Team',
	};

	const tabContent: Record<typeof tabs[number], string> = {
		presets: 'Presets panel content',
		dve: 'DVE panel content',
		keys: 'Keys panel content',
		video: 'Video processing panel content',
		encoder: 'Encoder panel content',
		format: 'Format panel content',
		team: 'Team panel content',
	};

	function handleKeydown(e: KeyboardEvent) {
		if (visible && e.code === 'Escape') {
			e.preventDefault();
			onclose();
		}
	}

	function handleBackdropClick(e: MouseEvent) {
		if (e.target === e.currentTarget) {
			onclose();
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} />

{#if visible}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="settings-backdrop" onclick={handleBackdropClick}>
		<div
			class="settings-modal"
			role="dialog"
			aria-modal="true"
			aria-label="Settings"
		>
			<header class="settings-header">
				<h2 class="settings-title">SETTINGS</h2>
				<button class="close-btn" onclick={onclose} title="Close settings">
					&times;
				</button>
			</header>

			<div class="tab-bar" role="tablist" aria-label="Settings tabs">
				{#each tabs as tab}
					<button
						class="tab"
						class:active={activeTab === tab}
						role="tab"
						aria-selected={activeTab === tab}
						onclick={() => { activeTab = tab; }}
					>
						{tabLabels[tab]}
					</button>
				{/each}
			</div>

			<div
				class="tab-content"
				role="tabpanel"
			>
				<div class="tab-placeholder">
					{tabContent[activeTab as typeof tabs[number]]}
				</div>
			</div>
		</div>
	</div>
{/if}

<style>
	.settings-backdrop {
		position: fixed;
		inset: 0;
		background: var(--overlay-heavy);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: var(--z-modal, 200);
	}

	.settings-modal {
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-lg);
		box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
		width: 80%;
		height: 70%;
		max-width: 900px;
		max-height: 600px;
		display: flex;
		flex-direction: column;
		overflow: hidden;
	}

	.settings-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border-default);
		flex-shrink: 0;
	}

	.settings-title {
		margin: 0;
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-primary);
	}

	.close-btn {
		background: none;
		border: 1px solid var(--border-default);
		color: var(--text-secondary);
		font-size: 18px;
		line-height: 1;
		cursor: pointer;
		padding: 2px 8px;
		border-radius: var(--radius-sm);
		transition:
			color var(--transition-fast),
			border-color var(--transition-fast);
	}

	.close-btn:hover {
		color: var(--text-primary);
		border-color: var(--border-strong);
	}

	/* ── Tab bar ── */
	.tab-bar {
		display: flex;
		gap: 0;
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-default);
		flex-shrink: 0;
		height: 30px;
	}

	.tab {
		font-family: var(--font-ui);
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-secondary);
		background: transparent;
		border: none;
		border-bottom: 2px solid transparent;
		padding: 0 14px;
		cursor: pointer;
		transition:
			color var(--transition-fast),
			border-color var(--transition-fast),
			background var(--transition-fast);
		display: flex;
		align-items: center;
		letter-spacing: 0.01em;
	}

	.tab:hover {
		color: var(--text-primary);
		background: rgba(255, 255, 255, 0.04);
	}

	.tab.active {
		color: var(--text-primary);
		font-weight: 600;
		border-bottom-color: var(--accent-yellow);
		background: rgba(255, 255, 255, 0.03);
	}

	/* ── Tab content ── */
	.tab-content {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}

	.tab-placeholder {
		padding: 24px;
		font-family: var(--font-ui);
		font-size: var(--text-md);
		color: var(--text-secondary);
	}
</style>

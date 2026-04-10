<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		visible: boolean;
		title: string;
		onclose: () => void;
		width?: number;
		children?: Snippet;
	}

	let { visible, title, onclose, width = 400, children }: Props = $props();
</script>

{#if visible}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="oa-slideout-backdrop" onclick={onclose}></div>
	<div class="oa-slideout" style:width="{width}px">
		<header class="oa-slideout-header">
			<span class="oa-slideout-title">{title}</span>
			<button class="oa-slideout-close" onclick={onclose} aria-label="Close">&times;</button>
		</header>
		<div class="oa-slideout-content">
			{#if children}
				{@render children()}
			{/if}
		</div>
	</div>
{/if}

<style>
	.oa-slideout-backdrop {
		position: fixed;
		inset: 0;
		background: var(--overlay-light);
		z-index: var(--z-overlay);
	}

	.oa-slideout {
		position: fixed;
		top: 0;
		right: 0;
		bottom: 0;
		display: flex;
		flex-direction: column;
		background: var(--bg-panel);
		border-left: 1px solid var(--border-strong);
		z-index: var(--z-overlay);
		animation: oa-slide-in 150ms ease forwards;
	}

	@keyframes oa-slide-in {
		from {
			transform: translateX(100%);
		}
		to {
			transform: translateX(0);
		}
	}

	.oa-slideout-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 6px 10px;
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-default);
		flex-shrink: 0;
	}

	.oa-slideout-title {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--text-secondary);
	}

	.oa-slideout-close {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 18px;
		line-height: 1;
		cursor: pointer;
		padding: 2px 6px;
		border-radius: var(--radius-sm);
		transition: color var(--transition-fast);
	}

	.oa-slideout-close:hover {
		color: var(--text-primary);
	}

	.oa-slideout-content {
		flex: 1;
		overflow-y: auto;
	}
</style>

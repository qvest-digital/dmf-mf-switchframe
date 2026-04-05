<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		title: string;
		expanded?: boolean;
		badge?: string;
		children: Snippet;
	}

	let { title, expanded = $bindable(false), badge, children }: Props = $props();

	function toggle() {
		expanded = !expanded;
	}
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="inspector-section" class:expanded>
	<div class="section-header" onclick={toggle}>
		<span class="chevron" class:expanded>&#x25B8;</span>
		<span class="section-title">{title}</span>
		{#if badge}
			<span class="badge">{badge}</span>
		{/if}
	</div>
	<div class="section-body" class:expanded>
		<div class="section-content">
			{@render children()}
		</div>
	</div>
</div>

<style>
	.inspector-section {
		border-bottom: 1px solid var(--border-subtle);
	}

	.inspector-section.expanded {
		border-bottom: none;
	}

	.section-header {
		display: flex;
		align-items: center;
		gap: 4px;
		height: 24px;
		padding: 0 8px;
		cursor: pointer;
		user-select: none;
		font-family: var(--font-ui);
	}

	.section-header:hover {
		background: var(--bg-hover, #2c2c32);
	}

	.chevron {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 10px;
		font-size: 10px;
		color: var(--text-tertiary);
		transition: transform var(--transition-fast, 150ms) ease-out;
	}

	.chevron.expanded {
		transform: rotate(90deg);
	}

	.section-title {
		flex: 1;
		font-size: 10px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.5px;
		color: var(--text-secondary);
		line-height: 1;
	}

	.badge {
		padding: 1px 5px;
		background: var(--bg-control, #242429);
		border-radius: var(--radius-xs, 3px);
		font-size: 9px;
		font-weight: 500;
		color: var(--text-tertiary);
		line-height: 1.2;
	}

	.section-body {
		display: grid;
		grid-template-rows: 0fr;
		opacity: 0;
		transition:
			grid-template-rows 200ms ease-out,
			opacity 200ms ease-out;
	}

	.section-body.expanded {
		grid-template-rows: 1fr;
		opacity: 1;
	}

	.section-content {
		overflow: hidden;
		padding-left: 4px;
	}
</style>

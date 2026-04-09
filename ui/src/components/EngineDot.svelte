<script lang="ts">
	type DotStatus = 'healthy' | 'degraded' | 'offline';

	interface Props {
		status: DotStatus;
		label?: string;
		title?: string;
		size?: 'sm' | 'md';
	}

	let { status, label, title, size = 'sm' }: Props = $props();
</script>

<span
	class="engine-dot {size}"
	class:healthy={status === 'healthy'}
	class:degraded={status === 'degraded'}
	class:offline={status === 'offline'}
	{title}
>
	{#if label}{label}{/if}
</span>

<style>
	.engine-dot {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		border-radius: 4px;
		font-weight: 700;
		line-height: 1;
		color: #fff;
		background: #555;
	}

	.engine-dot.sm {
		width: 20px;
		height: 20px;
		font-size: 0.65rem;
	}

	.engine-dot.md {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		margin-left: auto;
	}

	.engine-dot.healthy {
		background: var(--tally-preview, #22c55e);
	}

	.engine-dot.degraded {
		background: var(--accent-yellow, #eab308);
	}

	.engine-dot.offline {
		background: #ef4444;
	}
</style>

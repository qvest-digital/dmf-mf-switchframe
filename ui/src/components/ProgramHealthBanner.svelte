<script lang="ts">
	interface Props {
		programSource: string;
		status: string;
	}
	let { programSource, status }: Props = $props();
	let show = $derived(status !== 'healthy' && status !== '' && programSource !== '');
</script>

{#if show}
	<div class="program-health-banner" role="alert" aria-live="assertive">
		PROGRAM SOURCE "{programSource}" — {status.toUpperCase().replace('_', ' ')}
	</div>
{/if}

<style>
	.program-health-banner {
		position: fixed;
		top: 0;
		left: 0;
		right: 0;
		background: #cc0000;
		color: #fff;
		text-align: center;
		padding: 8px 16px;
		font-family: var(--font-ui);
		font-weight: 700;
		font-size: 0.85rem;
		letter-spacing: 0.06em;
		z-index: 1001;
		animation: flash-banner 1s infinite;
	}

	@keyframes flash-banner {
		0%, 100% { background: #cc0000; }
		50% { background: #ff2222; }
	}
</style>

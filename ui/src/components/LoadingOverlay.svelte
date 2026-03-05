<script lang="ts">
	interface Props {
		loading: boolean;
		error: string | null;
	}
	let { loading, error }: Props = $props();
</script>

{#if loading}
	<div class="loading-backdrop">
		<div class="loading-content">
			{#if error}
				<div class="error-icon">!</div>
				<h2 class="title">Server unavailable</h2>
				<p class="error-detail">{error}</p>
				<div class="retry-row">
					<div class="spinner spinner-small"></div>
					<span class="retry-text">Retrying...</span>
				</div>
			{:else}
				<div class="spinner"></div>
				<h2 class="title">Connecting to server...</h2>
			{/if}
		</div>
	</div>
{/if}

<style>
	.loading-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(9, 9, 11, 0.85);
		backdrop-filter: blur(8px);
		-webkit-backdrop-filter: blur(8px);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 300;
	}

	.loading-content {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 16px;
	}

	.title {
		font-family: var(--font-ui);
		font-size: 1rem;
		font-weight: 600;
		color: var(--text-primary);
		letter-spacing: 0.01em;
	}

	.error-icon {
		width: 48px;
		height: 48px;
		border-radius: 50%;
		background: rgba(220, 38, 38, 0.15);
		border: 2px solid var(--tally-program);
		display: flex;
		align-items: center;
		justify-content: center;
		font-family: var(--font-ui);
		font-size: 1.4rem;
		font-weight: 700;
		color: var(--tally-program);
	}

	.error-detail {
		font-family: var(--font-mono);
		font-size: 0.75rem;
		color: var(--text-secondary);
		max-width: 360px;
		text-align: center;
		line-height: 1.5;
	}

	.retry-row {
		display: flex;
		align-items: center;
		gap: 8px;
		margin-top: 4px;
	}

	.retry-text {
		font-family: var(--font-ui);
		font-size: 0.75rem;
		color: var(--text-tertiary);
	}

	.spinner {
		width: 36px;
		height: 36px;
		border: 3px solid var(--border-default);
		border-top-color: var(--text-primary);
		border-radius: 50%;
		animation: spin 0.8s linear infinite;
	}

	.spinner-small {
		width: 14px;
		height: 14px;
		border-width: 2px;
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
</style>

<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		children: Snippet;
	}

	let { children }: Props = $props();

	function handleError(error: unknown) {
		console.error('[ErrorBoundary] Component render error:', error);
	}
</script>

<svelte:boundary onerror={handleError}>
	{#snippet failed(error, reset)}
		<div class="error-boundary-overlay">
			<div class="error-boundary-card">
				<h2 class="error-title">Something went wrong</h2>
				<div class="error-message">
					{error instanceof Error ? error.message : String(error)}
				</div>
				<div class="error-actions">
					<button class="error-btn retry-btn" onclick={reset}>
						Try Again
					</button>
					<button class="error-btn reload-btn" onclick={() => window.location.reload()}>
						Reload Page
					</button>
				</div>
			</div>
		</div>
	{/snippet}

	{@render children?.()}
</svelte:boundary>

<style>
	.error-boundary-overlay {
		position: fixed;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--bg-base, #09090b);
		z-index: var(--z-system);
	}

	.error-boundary-card {
		max-width: 480px;
		width: 90%;
		padding: 32px;
		background: var(--bg-surface, #0f0f12);
		border: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
		border-radius: var(--radius-lg, 8px);
		text-align: center;
	}

	.error-title {
		font-family: var(--font-ui, system-ui, sans-serif);
		font-size: var(--text-xl);
		font-weight: 600;
		color: var(--text-primary, #e4e4e8);
		margin-bottom: 16px;
	}

	.error-message {
		font-family: var(--font-mono, monospace);
		font-size: var(--text-md);
		color: var(--text-secondary, #85858f);
		background: var(--bg-base, #09090b);
		border: 1px solid var(--border-subtle, rgba(255, 255, 255, 0.05));
		border-radius: var(--radius-md, 5px);
		padding: 12px 16px;
		margin-bottom: 24px;
		word-break: break-word;
		max-height: 120px;
		overflow-y: auto;
		text-align: left;
	}

	.error-actions {
		display: flex;
		gap: 12px;
		justify-content: center;
	}

	.error-btn {
		padding: 10px 24px;
		font-family: var(--font-ui, system-ui, sans-serif);
		font-size: var(--text-md);
		font-weight: 600;
		border: 1.5px solid;
		border-radius: var(--radius-md, 5px);
		cursor: pointer;
		transition:
			background var(--transition-fast, 100ms ease),
			border-color var(--transition-fast, 100ms ease),
			box-shadow var(--transition-normal, 150ms ease);
	}

	.error-btn:active {
		transform: scale(0.97);
	}

	.retry-btn {
		background: var(--accent-blue-dim, rgba(59, 130, 246, 0.12));
		color: var(--accent-blue, #3b82f6);
		border-color: var(--accent-blue, #3b82f6);
	}

	.retry-btn:hover {
		background: rgba(59, 130, 246, 0.25);
		box-shadow: 0 0 12px rgba(59, 130, 246, 0.2);
	}

	.reload-btn {
		background: var(--bg-elevated, #1c1c21);
		color: var(--text-secondary, #85858f);
		border-color: var(--border-default, rgba(255, 255, 255, 0.08));
	}

	.reload-btn:hover {
		color: var(--text-primary, #e4e4e8);
		border-color: var(--border-strong, rgba(255, 255, 255, 0.14));
		background: var(--bg-hover, #2c2c32);
	}
</style>

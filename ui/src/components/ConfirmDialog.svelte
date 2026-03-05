<script lang="ts">
	interface Props {
		open: boolean;
		title: string;
		message: string;
		confirmLabel?: string;
		onconfirm: () => void;
		oncancel: () => void;
	}
	let { open, title, message, confirmLabel = 'Confirm', onconfirm, oncancel }: Props = $props();

	function handleKeydown(e: KeyboardEvent) {
		if (open && e.code === 'Escape') {
			e.preventDefault();
			oncancel();
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="confirm-backdrop" onkeydown={() => {}}>
		<div
			class="confirm-dialog"
			role="alertdialog"
			aria-modal="true"
			aria-labelledby="confirm-title"
			aria-describedby="confirm-message"
		>
			<h3 id="confirm-title">{title}</h3>
			<p id="confirm-message">{message}</p>
			<div class="confirm-actions">
				<button class="cancel-btn" onclick={oncancel}>Cancel</button>
				<button class="confirm-btn" onclick={onconfirm}>{confirmLabel}</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.confirm-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		backdrop-filter: blur(4px);
		-webkit-backdrop-filter: blur(4px);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 200;
	}

	.confirm-dialog {
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-lg);
		padding: 20px 24px;
		min-width: 300px;
		max-width: 380px;
		font-family: var(--font-ui);
		color: var(--text-primary);
		box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
	}

	h3 {
		margin: 0 0 8px 0;
		font-size: 0.9rem;
		font-weight: 600;
		color: var(--text-primary);
	}

	p {
		margin: 0 0 20px 0;
		font-size: 0.8rem;
		color: var(--text-secondary);
		line-height: 1.4;
	}

	.confirm-actions {
		display: flex;
		gap: 8px;
		justify-content: flex-end;
	}

	.cancel-btn,
	.confirm-btn {
		padding: 7px 16px;
		border-radius: var(--radius-md);
		cursor: pointer;
		font-family: var(--font-ui);
		font-weight: 600;
		font-size: 0.8rem;
		letter-spacing: 0.02em;
		transition:
			border-color var(--transition-fast),
			background var(--transition-fast);
	}

	.cancel-btn {
		border: 1px solid var(--border-default);
		background: var(--bg-elevated);
		color: var(--text-secondary);
	}

	.cancel-btn:hover {
		border-color: var(--border-strong);
		color: var(--text-primary);
	}

	.confirm-btn {
		border: 1px solid rgba(220, 38, 38, 0.4);
		background: var(--tally-program-dim);
		color: var(--tally-program);
	}

	.confirm-btn:hover {
		background: rgba(220, 38, 38, 0.25);
	}
</style>

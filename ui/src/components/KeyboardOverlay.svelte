<script lang="ts">
	interface Props {
		onclose: () => void;
	}
	let { onclose }: Props = $props();

	const shortcuts = [
		{ key: '1-9', action: 'Select preview source' },
		{ key: 'Shift + 1-9', action: 'Hot-punch to program' },
		{ key: 'Space', action: 'Cut (swap preview → program)' },
		{ key: 'Enter', action: 'Auto transition (mix/dip)' },
		{ key: 'F1', action: 'Fade to black' },
		{ key: '` (backtick)', action: 'Toggle fullscreen' },
		{ key: '?', action: 'Toggle this overlay' },
		{ key: 'Esc', action: 'Close overlay' },
	];

	function handleKeydown(e: KeyboardEvent) {
		if (e.code === 'Escape' || e.code === 'Slash') {
			e.preventDefault();
			onclose();
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="overlay-backdrop" onclick={onclose} role="presentation">
	<div class="overlay" onclick={(e) => e.stopPropagation()} onkeydown={handleKeydown} role="dialog" aria-modal="true" aria-label="Keyboard shortcuts" tabindex="-1">
		<h2>Keyboard Shortcuts</h2>
		<table>
			<thead>
				<tr><th>Key</th><th>Action</th></tr>
			</thead>
			<tbody>
				{#each shortcuts as shortcut}
					<tr>
						<td class="key"><kbd>{shortcut.key}</kbd></td>
						<td>{shortcut.action}</td>
					</tr>
				{/each}
			</tbody>
		</table>
		<p class="dismiss">Press <kbd>?</kbd> or <kbd>Esc</kbd> to close</p>
	</div>
</div>

<style>
	.overlay-backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.6);
		backdrop-filter: blur(4px);
		-webkit-backdrop-filter: blur(4px);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: 100;
	}

	.overlay {
		background: var(--bg-panel);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-lg);
		padding: 24px;
		max-width: 460px;
		width: 90%;
		box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
	}

	h2 {
		margin-bottom: 16px;
		font-family: var(--font-ui);
		font-size: 1rem;
		font-weight: 600;
		color: var(--text-primary);
		letter-spacing: 0.01em;
	}

	table {
		width: 100%;
		border-collapse: collapse;
	}

	th {
		text-align: left;
		padding: 6px 0;
		border-bottom: 1px solid var(--border-default);
		font-family: var(--font-ui);
		font-size: 0.65rem;
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	td {
		padding: 7px 0;
		font-family: var(--font-ui);
		font-size: 0.8rem;
		color: var(--text-secondary);
		border-bottom: 1px solid var(--border-subtle);
	}

	tr:last-child td {
		border-bottom: none;
	}

	.key {
		width: 40%;
	}

	kbd {
		background: var(--bg-control);
		border: 1px solid var(--border-default);
		border-bottom-width: 2px;
		border-radius: var(--radius-sm);
		padding: 2px 7px;
		font-family: var(--font-mono);
		font-size: 0.7rem;
		font-weight: 500;
		color: var(--text-primary);
	}

	.dismiss {
		margin-top: 14px;
		text-align: center;
		font-size: 0.7rem;
		color: var(--text-tertiary);
		font-family: var(--font-ui);
	}

	.dismiss kbd {
		font-size: 0.6rem;
	}
</style>

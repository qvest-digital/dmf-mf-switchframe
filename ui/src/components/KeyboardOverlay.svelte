<script lang="ts">
	interface Props {
		onclose: () => void;
	}
	let { onclose }: Props = $props();

	const shortcuts = [
		// Switching
		{ key: '1-9', action: 'Select preview source' },
		{ key: 'Shift + 1-9', action: 'Hot-punch to program' },
		{ key: 'Space', action: 'Cut (swap preview → program)' },
		// Transitions
		{ key: 'Enter', action: 'Auto transition (mix/dip)' },
		{ key: 'F1', action: 'Fade to black' },
		{ key: 'F2', action: 'Toggle DSK' },
		// Replay
		{ key: 'Shift + 1/2/3', action: 'Quick replay preset (global)' },
		{ key: 'I', action: 'Mark in (replay tab)' },
		{ key: 'O', action: 'Mark out (replay tab)' },
		{ key: 'Space', action: 'Play / Pause (replay tab)' },
		{ key: 'Esc', action: 'Stop (replay tab)' },
		{ key: 'J', action: 'Slower (replay tab)' },
		{ key: 'K', action: 'Play / Pause (replay tab)' },
		{ key: 'L', action: 'Faster (replay tab)' },
		{ key: '← →', action: 'Frame step (replay tab, paused)' },
		// Macros
		{ key: 'Ctrl + 1-9', action: 'Run macro' },
		// Tabs
		{ key: 'Ctrl+Shift + 1-7', action: 'Switch bottom tab' },
		// Layout
		{ key: 'P', action: 'Toggle PIP layout' },
		{ key: 'Shift + P', action: 'Pipeline stats panel' },
		{ key: 'Shift + G', action: 'Pipeline graph' },
		{ key: 'Shift + I', action: 'I/O management panel' },
		// Misc
		{ key: '` (backtick)', action: 'Toggle comms mute' },
		{ key: '?', action: 'Toggle this overlay' },
		{ key: 'Esc', action: 'Close overlay' },
		{ key: 'Ctrl+Shift + D', action: 'Export debug snapshot' },
		// SCTE-35
		{ key: 'Shift + B', action: 'Ad break (SCTE-35)' },
		{ key: 'Shift + R', action: 'Return to program (SCTE-35)' },
		{ key: 'Shift + H', action: 'Hold break (SCTE-35)' },
		{ key: 'Shift + E', action: 'Extend break (SCTE-35)' },
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
		background: var(--overlay-heavy);
		backdrop-filter: blur(4px);
		-webkit-backdrop-filter: blur(4px);
		display: flex;
		align-items: center;
		justify-content: center;
		z-index: var(--z-overlay);
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
		font-size: var(--text-base);
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
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--text-tertiary);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	td {
		padding: 7px 0;
		font-family: var(--font-ui);
		font-size: var(--text-md);
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
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--text-primary);
	}

	.dismiss {
		margin-top: 14px;
		text-align: center;
		font-size: var(--text-sm);
		color: var(--text-tertiary);
		font-family: var(--font-ui);
	}

	.dismiss kbd {
		font-size: var(--text-xs);
	}
</style>

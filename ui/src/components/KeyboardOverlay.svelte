<script lang="ts">
	interface Props {
		onclose: () => void;
	}
	let { onclose }: Props = $props();

	const shortcuts = [
		{ key: '1-9', action: 'Select preview source' },
		{ key: 'Shift + 1-9', action: 'Hot-punch to program' },
		{ key: 'Space', action: 'Cut (swap preview → program)' },
		{ key: 'Enter', action: 'Auto transition (Phase 4)' },
		{ key: 'F1', action: 'Fade to black (deferred)' },
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
	<div class="overlay" onclick={(e) => e.stopPropagation()} onkeydown={handleKeydown} role="dialog" aria-label="Keyboard shortcuts" tabindex="-1">
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
	.overlay-backdrop { position: fixed; inset: 0; background: rgba(0,0,0,0.7); display: flex; align-items: center; justify-content: center; z-index: 100; }
	.overlay { background: #222; border: 1px solid #444; border-radius: 8px; padding: 2rem; max-width: 500px; width: 90%; }
	h2 { margin-bottom: 1rem; font-family: monospace; font-size: 1.2rem; }
	table { width: 100%; border-collapse: collapse; }
	th { text-align: left; padding: 0.3rem 0; border-bottom: 1px solid #444; font-family: monospace; font-size: 0.8rem; color: #888; }
	td { padding: 0.4rem 0; font-family: monospace; font-size: 0.85rem; }
	.key { width: 40%; }
	kbd { background: #333; border: 1px solid #555; border-radius: 3px; padding: 0.1rem 0.4rem; font-family: monospace; font-size: 0.8rem; }
	.dismiss { margin-top: 1rem; text-align: center; font-size: 0.75rem; color: #666; }
</style>

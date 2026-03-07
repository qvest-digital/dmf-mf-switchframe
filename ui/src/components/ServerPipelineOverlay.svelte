<script lang="ts">
	let visible = $state(false);
	let snapshot = $state<Record<string, any> | null>(null);
	let intervalId: ReturnType<typeof setInterval> | undefined;

	function toggle() {
		visible = !visible;
		if (visible) {
			poll();
			intervalId = setInterval(poll, 2500);
		} else {
			if (intervalId) { clearInterval(intervalId); intervalId = undefined; }
		}
	}

	async function poll() {
		try {
			const resp = await fetch('/api/debug/snapshot');
			if (resp.ok) snapshot = await resp.json();
		} catch { /* ignore network errors */ }
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.shiftKey && !e.ctrlKey && !e.metaKey && e.code === 'KeyP') {
			// Shift+P toggles this overlay
			// Don't prevent default — only capture when not in an input
			if ((e.target as HTMLElement)?.tagName === 'INPUT' || (e.target as HTMLElement)?.tagName === 'TEXTAREA') return;
			e.preventDefault();
			toggle();
		}
	}

	$effect(() => {
		document.addEventListener('keydown', handleKeydown);
		return () => {
			document.removeEventListener('keydown', handleKeydown);
			if (intervalId) { clearInterval(intervalId); intervalId = undefined; }
		};
	});

	function fmt(v: number | undefined, decimals = 1): string {
		if (v === undefined || v === null) return '-';
		return v.toFixed(decimals);
	}

	function warn(condition: boolean): string {
		return condition ? 'warn' : '';
	}

	function crit(condition: boolean): string {
		return condition ? 'crit' : '';
	}
</script>

{#if visible}
	<div class="server-pipeline-overlay" role="status" aria-label="Server pipeline metrics">
		<div class="overlay-header">
			<span>SERVER PIPELINE</span>
			<button class="close-btn" onclick={() => { visible = false; if (intervalId) { clearInterval(intervalId); intervalId = undefined; } }}>x</button>
		</div>
		{#if snapshot}
			{@const pipe = snapshot.switcher?.video_pipeline}
			{@const trans = snapshot.switcher?.transition_engine}
			{@const mixer = snapshot.mixer}
			{#if pipe}
				<table>
					<tbody>
						<tr><td colspan="3" class="section">OUTPUT</td></tr>
						<tr class={crit((pipe.output_fps ?? 0) < 25 && (pipe.output_fps ?? 0) > 0)}>
							<td>FPS</td><td class="val">{pipe.output_fps ?? '-'}</td><td></td>
						</tr>
						<tr>
							<td>Processed</td><td class="val">{pipe.frames_processed ?? 0}</td><td></td>
						</tr>
						<tr>
							<td>Broadcast</td><td class="val">{pipe.frames_broadcast ?? 0}</td><td></td>
						</tr>
						<tr class={crit((pipe.frames_dropped ?? 0) > 0)}>
							<td>Dropped</td><td class="val">{pipe.frames_dropped ?? 0}</td><td></td>
						</tr>
						<tr>
							<td>Queue</td><td class="val">{pipe.queue_len ?? 0}/4</td><td></td>
						</tr>

						<tr><td colspan="3" class="section">STAGE TIMING (last / max)</td></tr>
						<tr class={warn((pipe.decode_max_ms ?? 0) > 33)}>
							<td>Decode</td>
							<td class="val">{fmt(pipe.decode_last_ms)}ms</td>
							<td class="val">{fmt(pipe.decode_max_ms)}ms</td>
						</tr>
						<tr>
							<td>Key</td>
							<td class="val">{fmt(pipe.key_last_ms)}ms</td>
							<td class="val">{fmt(pipe.key_max_ms)}ms</td>
						</tr>
						<tr>
							<td>Composite</td>
							<td class="val">{fmt(pipe.composite_last_ms)}ms</td>
							<td class="val">{fmt(pipe.composite_max_ms)}ms</td>
						</tr>
						<tr class={warn((pipe.encode_max_ms ?? 0) > 33)}>
							<td>Encode</td>
							<td class="val">{fmt(pipe.encode_last_ms)}ms</td>
							<td class="val">{fmt(pipe.encode_max_ms)}ms</td>
						</tr>
						<tr class={crit((pipe.max_proc_time_ms ?? 0) > 33)}>
							<td>Total</td>
							<td class="val">{fmt(pipe.last_proc_time_ms)}ms</td>
							<td class="val">{fmt(pipe.max_proc_time_ms)}ms</td>
						</tr>

						<tr><td colspan="3" class="section">BROADCAST</td></tr>
						<tr class={warn((pipe.max_broadcast_gap_ms ?? 0) > 100)}>
							<td>Max gap</td><td class="val">{fmt(pipe.max_broadcast_gap_ms)}ms</td><td></td>
						</tr>
						<tr>
							<td>Route: engine</td><td class="val">{pipe.route_to_engine ?? 0}</td><td></td>
						</tr>
						<tr>
							<td>Route: pipeline</td><td class="val">{pipe.route_to_pipeline ?? 0}</td><td></td>
						</tr>
						<tr>
							<td>Route: filtered</td><td class="val">{pipe.route_filtered ?? 0}</td><td></td>
						</tr>
					</tbody>
				</table>

				{#if trans}
					<table>
						<tbody>
							<tr><td colspan="3" class="section">TRANSITION ENGINE</td></tr>
							<tr class={crit((trans.ingest_max_ms ?? 0) > 33)}>
								<td>Ingest</td>
								<td class="val">{fmt(trans.ingest_last_ms)}ms</td>
								<td class="val">{fmt(trans.ingest_max_ms)}ms</td>
							</tr>
							<tr>
								<td>Decode</td>
								<td class="val">{fmt(trans.decode_last_ms)}ms</td>
								<td class="val">{fmt(trans.decode_max_ms)}ms</td>
							</tr>
							<tr>
								<td>Blend</td>
								<td class="val">{fmt(trans.blend_last_ms)}ms</td>
								<td class="val">{fmt(trans.blend_max_ms)}ms</td>
							</tr>
							<tr>
								<td>Ingested</td><td class="val">{trans.frames_ingested ?? 0}</td><td></td>
							</tr>
							<tr>
								<td>Blended</td><td class="val">{trans.frames_blended ?? 0}</td><td></td>
							</tr>
						</tbody>
					</table>
				{/if}

				{#if mixer}
					<table>
						<tbody>
							<tr><td colspan="3" class="section">AUDIO MIXER</td></tr>
							<tr>
								<td>Mode</td><td class="val">{mixer.mode ?? '-'}</td><td></td>
							</tr>
							<tr>
								<td>Mixed</td><td class="val">{mixer.frames_mixed ?? 0}</td><td></td>
							</tr>
							<tr>
								<td>Passthrough</td><td class="val">{mixer.frames_passthrough ?? 0}</td><td></td>
							</tr>
							<tr class={warn((mixer.max_inter_frame_gap_ms ?? 0) > 50)}>
								<td>Max gap</td><td class="val">{fmt(mixer.max_inter_frame_gap_ms)}ms</td><td></td>
							</tr>
						</tbody>
					</table>
				{/if}
			{/if}
		{:else}
			<div class="loading">Loading...</div>
		{/if}
	</div>
{/if}

<style>
	.server-pipeline-overlay {
		position: fixed;
		top: 8px;
		right: 8px;
		z-index: 9999;
		background: rgba(0, 0, 0, 0.88);
		border: 1px solid #444;
		border-radius: 6px;
		padding: 8px 12px;
		font-family: 'SF Mono', 'Menlo', 'Monaco', monospace;
		font-size: 11px;
		color: #ccc;
		pointer-events: auto;
		max-height: 90vh;
		overflow-y: auto;
	}

	.overlay-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		color: #fff;
		font-weight: 600;
		font-size: 11px;
		margin-bottom: 6px;
		letter-spacing: 0.5px;
	}

	.close-btn {
		background: none;
		border: none;
		color: #888;
		cursor: pointer;
		font-family: inherit;
		font-size: 12px;
		padding: 0 2px;
	}

	.close-btn:hover { color: #fff; }

	table {
		border-collapse: collapse;
		width: 100%;
		margin-bottom: 4px;
	}

	td {
		padding: 1px 6px 1px 0;
		white-space: nowrap;
	}

	.val {
		text-align: right;
		font-variant-numeric: tabular-nums;
		color: #8f8;
	}

	.section {
		color: #aaa;
		font-weight: 600;
		padding-top: 6px;
		font-size: 10px;
		letter-spacing: 0.5px;
	}

	tr.warn .val { color: #fc0; }
	tr.crit .val { color: #f44; }

	.loading {
		color: #888;
		padding: 8px 0;
	}
</style>

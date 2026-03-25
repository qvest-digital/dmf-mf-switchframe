<script lang="ts">
	import type { PodStatusBroadcast, PodTemplateDef } from '$lib/api/types';
	import { podCreate, podArm, podFire, podDump, podHold, podExtend } from '$lib/api/switch-api';

	interface Props {
		channel: number;
		activePod?: PodStatusBroadcast;
		pods?: PodStatusBroadcast[];
		podsArmed?: number;
		podTemplates?: PodTemplateDef[];
		showWarning?: boolean;
	}
	let { channel, activePod, pods = [], podsArmed = 0, podTemplates, showWarning = false }: Props = $props();

	const armedPods = $derived(
		pods.filter(p => p.armed && p.state !== 'break_active').sort((a, b) => a.id.localeCompare(b.id))
	);
	const hasActive = $derived(!!activePod && activePod.state === 'break_active');

	function formatDuration(ms: number): string {
		if (!ms || ms <= 0) return '--:--';
		const totalSecs = Math.ceil(ms / 1000);
		const m = Math.floor(totalSecs / 60);
		const s = totalSecs % 60;
		return `${m}:${s.toString().padStart(2, '0')}`;
	}

	function progressPct(pod: PodStatusBroadcast): number {
		if (!pod.durationMs || pod.durationMs <= 0) return 0;
		const elapsed = pod.elapsedMs ?? 0;
		return Math.min(100, (elapsed / pod.durationMs) * 100);
	}

	async function handleCreate(template: string) {
		try {
			const pod = await podCreate(channel, { template });
			await podArm(channel, pod.id);
		} catch (e) {
			console.error('pod create failed:', e);
		}
	}

	async function handleFire(podId: string) {
		try { await podFire(channel, podId); } catch (e) { console.error('pod fire failed:', e); }
	}

	async function handleDump(podId: string) {
		try { await podDump(channel, podId); } catch (e) { console.error('pod dump failed:', e); }
	}

	async function handleHold(podId: string) {
		try { await podHold(channel, podId); } catch (e) { console.error('pod hold failed:', e); }
	}

	async function handleExtend(podId: string) {
		try { await podExtend(channel, podId, 30000); } catch (e) { console.error('pod extend failed:', e); }
	}
</script>

<div class="pod-board" class:break-active={hasActive}>
	<div class="pod-header">
		<span class="pod-title">{hasActive ? 'BREAK ACTIVE' : 'POD BOARD'}</span>
		{#if podsArmed > 0}
			<span class="armed-badge">{podsArmed} armed</span>
		{:else if showWarning}
			<span class="no-pods-warning">NO PODS ARMED</span>
		{/if}
	</div>

	{#if hasActive && activePod}
		<div class="active-section">
			<div class="active-label">ACTIVE</div>
			<div class="active-pod">
				<div class="active-info">
					<span class="pod-type">{activePod.type}</span>
					<span class="pod-id">{activePod.id.slice(-6)}</span>
				</div>
				<div class="active-progress">
					<div class="progress-track">
						<div class="progress-fill" style:width="{progressPct(activePod)}%"></div>
					</div>
				</div>
				<div class="active-time">
					<span class="countdown">{formatDuration(activePod.remainingMs ?? 0)}</span>
					<span class="elapsed">/{formatDuration(activePod.durationMs)}</span>
				</div>
				<div class="active-actions">
					<button class="action-btn dump" onclick={() => handleDump(activePod!.id)}>DUMP</button>
					<button class="action-btn hold" onclick={() => handleHold(activePod!.id)}>HOLD</button>
					<button class="action-btn extend" onclick={() => handleExtend(activePod!.id)}>+30s</button>
				</div>
			</div>
		</div>
	{/if}

	{#if armedPods.length > 0}
		<div class="armed-section">
			<div class="section-label">ARMED</div>
			{#each armedPods as pod}
				<div class="armed-pod">
					<span class="pod-type">{pod.type}</span>
					<span class="pod-dur">{formatDuration(pod.durationMs)}</span>
					<button class="icon-btn fire" onclick={() => handleFire(pod.id)} title="Fire now">&#9654;</button>
				</div>
			{/each}
		</div>
	{/if}

	<div class="template-buttons">
		{#if podTemplates && podTemplates.length > 0}
			{#each podTemplates as tmpl}
				<button class="action-btn tmpl" onclick={() => handleCreate(tmpl.label)}>+{tmpl.label}</button>
			{/each}
		{:else}
			<button class="action-btn tmpl" onclick={() => handleCreate('short')}>+Short</button>
			<button class="action-btn tmpl" onclick={() => handleCreate('standard')}>+Standard</button>
			<button class="action-btn tmpl" onclick={() => handleCreate('long')}>+Long</button>
		{/if}
	</div>
</div>

<style>
	.pod-board {
		display: flex;
		flex-direction: column;
		gap: 4px;
		border: 2px solid color-mix(in srgb, orange 30%, var(--border-default));
		border-radius: var(--radius-sm);
		padding: 4px 6px;
		background: color-mix(in srgb, orange 3%, var(--bg-surface));
		transition: border-color 0.3s, background 0.3s, border-width 0.2s;
	}

	.pod-board.break-active {
		border: 3px solid color-mix(in srgb, orange 60%, var(--border-default));
		background: color-mix(in srgb, orange 6%, var(--bg-surface));
		animation: break-border-pulse 2s ease-in-out infinite;
	}

	@keyframes break-border-pulse {
		0%, 100% { border-color: color-mix(in srgb, orange 60%, var(--border-default)); }
		50% { border-color: color-mix(in srgb, orange 80%, var(--border-default)); }
	}

	.pod-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.pod-title {
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 700;
		color: orange;
		letter-spacing: 0.08em;
	}

	.armed-badge {
		font-family: var(--font-mono);
		font-size: 9px;
		color: orange;
		background: color-mix(in srgb, orange 20%, var(--bg-base));
		padding: 1px 4px;
		border-radius: 2px;
		font-weight: 700;
	}

	.no-pods-warning {
		font-family: var(--font-ui);
		font-size: 9px;
		color: color-mix(in srgb, orange 60%, var(--text-tertiary));
		font-weight: 600;
		letter-spacing: 0.04em;
	}

	/* -- Active pod -- */
	.active-section {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}

	.active-label, .section-label {
		font-family: var(--font-ui);
		font-size: 9px;
		font-weight: 700;
		letter-spacing: 0.08em;
		color: var(--text-tertiary);
	}

	.active-pod {
		display: flex;
		flex-direction: column;
		gap: 3px;
		background: color-mix(in srgb, orange 8%, var(--bg-base));
		border: 1px solid color-mix(in srgb, orange 40%, var(--border-default));
		border-radius: var(--radius-sm);
		padding: 4px 6px;
	}

	.active-info {
		display: flex;
		gap: 4px;
		align-items: center;
	}

	.pod-type {
		font-family: var(--font-ui);
		font-size: var(--text-xs);
		font-weight: 600;
		color: orange;
		text-transform: capitalize;
	}

	.pod-id {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
	}

	.active-progress .progress-track {
		height: 4px;
		background: var(--bg-base);
		border-radius: 2px;
		overflow: hidden;
	}

	.active-progress .progress-fill {
		height: 100%;
		background: orange;
		border-radius: 2px;
		transition: width 0.2s linear;
	}

	.active-time {
		display: flex;
		align-items: baseline;
		gap: 2px;
	}

	.countdown {
		font-family: var(--font-mono);
		font-size: 16px;
		font-weight: 700;
		color: var(--text-primary);
	}

	.elapsed {
		font-family: var(--font-mono);
		font-size: 10px;
		color: var(--text-tertiary);
	}

	.active-actions {
		display: flex;
		gap: 3px;
	}

	/* -- Armed list -- */
	.armed-section {
		display: flex;
		flex-direction: column;
		gap: 2px;
	}

	.armed-pod {
		display: flex;
		align-items: center;
		gap: 4px;
		padding: 2px 4px;
		background: var(--bg-base);
		border-radius: 2px;
	}

	.armed-pod .pod-type {
		flex: 1;
		font-size: var(--text-2xs);
	}

	.pod-dur {
		font-family: var(--font-mono);
		font-size: 9px;
		color: var(--text-tertiary);
	}

	/* -- Buttons -- */
	.action-btn {
		padding: 2px 6px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		background: var(--bg-surface);
		color: var(--text-secondary);
		font-family: var(--font-ui);
		font-size: var(--text-2xs);
		font-weight: 600;
		cursor: pointer;
		transition: background 0.1s, border-color 0.1s;
	}

	.action-btn:hover {
		background: var(--bg-hover);
		border-color: var(--border-emphasis);
	}

	.action-btn.dump {
		background: rgba(220, 38, 38, 0.1);
		color: var(--tally-program);
		border-color: rgba(220, 38, 38, 0.4);
		font-size: var(--text-xs);
		padding: 3px 10px;
		font-weight: 700;
	}

	.action-btn.dump:hover {
		background: color-mix(in srgb, var(--tally-program) 15%, var(--bg-surface));
	}

	.action-btn.hold {
		color: orange;
		border-color: color-mix(in srgb, orange 40%, transparent);
	}

	.action-btn.extend {
		color: var(--text-secondary);
	}

	.icon-btn {
		width: 18px;
		height: 18px;
		display: flex;
		align-items: center;
		justify-content: center;
		border: 1px solid var(--border-default);
		border-radius: 2px;
		background: var(--bg-surface);
		color: var(--text-secondary);
		font-size: 8px;
		cursor: pointer;
		flex-shrink: 0;
	}

	.icon-btn.fire {
		width: 22px;
		height: 22px;
		color: orange;
		border-color: orange;
		font-size: 10px;
	}

	.icon-btn.fire:hover {
		background: color-mix(in srgb, orange 25%, var(--bg-surface));
		transform: scale(1.1);
	}

	/* -- Template buttons -- */
	.template-buttons {
		display: flex;
		gap: 4px;
		padding-top: 4px;
		border-top: 1px solid color-mix(in srgb, orange 15%, var(--border-default));
	}

	.action-btn.tmpl {
		flex: 1;
		text-align: center;
		font-size: 10px;
		padding: 4px 4px;
		color: orange;
		border-color: color-mix(in srgb, orange 25%, var(--border-default));
	}

	.action-btn.tmpl:hover {
		background: color-mix(in srgb, orange 8%, var(--bg-surface));
	}

	/* -- Break-active overrides -- */
	.break-active .countdown {
		font-size: 22px;
	}

	.break-active .elapsed {
		font-size: 12px;
	}

	.break-active .active-progress .progress-track {
		height: 6px;
	}
</style>

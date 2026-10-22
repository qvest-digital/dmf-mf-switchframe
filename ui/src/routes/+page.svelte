<script lang="ts">
	import { onMount, onDestroy, tick } from 'svelte';
	import ProgramPreview from '../components/ProgramPreview.svelte';
	import SourceButton from '../components/SourceButton.svelte';
	import ContextualActions from '../components/ContextualActions.svelte';
	import TransitionControls from '../components/TransitionControls.svelte';
	import AudioMixer from '../components/AudioMixer.svelte';
	import OutputControls from '../components/OutputControls.svelte';
	import KeyboardOverlay from '../components/KeyboardOverlay.svelte';
	import LoadingOverlay from '../components/LoadingOverlay.svelte';
	import ConnectionBanner from '../components/ConnectionBanner.svelte';
	import ProgramHealthBanner from '../components/ProgramHealthBanner.svelte';
	import SimpleMode from '../components/SimpleMode.svelte';
	import OnAirController from '../components/OnAirController.svelte';
	import ErrorBoundary from '../components/ErrorBoundary.svelte';
	import Toast from '../components/Toast.svelte';
	import StatsPanel from '../components/StatsPanel.svelte';
	import PipelineGraph from '../components/PipelineGraph.svelte';
	import IOPanel from '../components/IOPanel.svelte';
	import CompositionPanel from '../components/CompositionPanel.svelte';
	import MacroPanel from '../components/MacroPanel.svelte';
	import ReplayPanel from '../components/ReplayPanel.svelte';
	import SCTE35Panel from '../components/SCTE35Panel.svelte';
	import CaptionsPanel from '../components/CaptionsPanel.svelte';
	import ClipsPanel from '../components/ClipsPanel.svelte';
	import PlayoutPanel from '../components/PlayoutPanel.svelte';
	import OperatorRegistration from '../components/OperatorRegistration.svelte';
	import OperatorBadge from '../components/OperatorBadge.svelte';
	import TeamPanel from '../components/TeamPanel.svelte';
	import VideoProcessingPanel from '../components/VideoProcessingPanel.svelte';
	import CommsBar from '../components/CommsBar.svelte';
	import BottomTabs from '../components/BottomTabs.svelte';
	import RedundancyPanel from '../components/RedundancyPanel.svelte';
	import ProgramCompare from '../components/ProgramCompare.svelte';
	import { createControlRoomStore } from '$lib/state/control-room.svelte';
	import { cutToPreview, autoTransitionToPreview } from '$lib/state/switch-actions';
	import { cut, setPreview, setLabel, startTransition, fadeToBlack, graphicsOn, graphicsOff, graphicsAutoOn, graphicsAutoOff, apiCall, setAuthToken, checkFragmentToken, SwitchApiError, listMacros, runMacro, dveSlotOn, dveSlotOff, replayQuick, replayPause, replayResume, replayPlay, replayStop, replaySeek, replaySetSpeed, replayMarkIn, replayMarkOut, commsMute, setDualTimestampProvider, setPartialFailureCallback, setPartialFailureNotify, setNonIdempotentFailureCallback, getDebugSnapshot, getPerfData, playoutEngineCacheStatus, setTransitionPosition, updateDVESlot, scte35Cue, scte35Return, podFire, playoutNext, playoutJump, playoutTakeManual, playoutReturnToAuto, startRecording, stopRecording, playoutPlaylist as fetchPlaylist } from '$lib/api/switch-api';
	import { setEngineUrls, getEngineUrls, setExpectDualEngine, setOnNewEngineDiscovered, setAuthoritativeEngineProvider, setEmergencyEngineBUrl } from '$lib/api/base-url';
	import { fetchServerInfo } from '$lib/prism/transport-utils';
	import * as operatorState from '$lib/state/operator.svelte';
	import { notify } from '$lib/state/notifications.svelte';
	import { getSourceError } from '$lib/transport/source-errors.svelte';
	import { KeyboardHandler, isEditableTarget } from '$lib/keyboard/handler';
	import { DualConnectionManager } from '$lib/transport/dual-connection';
	import { DualApiClient } from '$lib/api/dual-api-client';
	import { createMediaPipeline } from '$lib/transport/media-pipeline';
	import { PipelineManager } from '$lib/pipeline/manager';
	import { createPFLManager } from '$lib/audio/pfl';
	import { createPFLToggle } from '$lib/audio/pfl-toggle';
	import { createDivergenceLog } from '$lib/state/divergence-log.svelte';
	import { createAutoReconcile } from '$lib/state/auto-reconcile';
	import { detectAudioConfigDrift } from '$lib/util/audio-drift';
	import { getLayoutMode, setLayoutMode, type LayoutMode } from '$lib/layout/preferences';
	import { createFastControl, type FastControl } from '$lib/transport/fast-control';
	import { createDualFastControl } from '$lib/transport/dual-fast-control';
	import { sortedSourceKeys } from '$lib/util/sort-sources';
	import { inferEngineBUrl } from '$lib/util/engine-url';
	import { ControlPlaneDiscovery } from '$lib/transport/control-plane-discovery';
	import { CaptionRenderer } from '$lib/prism/captions';
	import type { ControlRoomState, Macro } from '$lib/api/types';

	// Extract token from URL fragment BEFORE any connections are created.
	// Must run at module level, not in onMount, to avoid race conditions
	// where API calls fire before the token is stored.
	checkFragmentToken();

	// Engine A is always the current server. Engine B is discovered from
	// the initial state response (engineBUrl field set by the control plane).
	const engineConfig: Array<{ id: import('$lib/api/types').EngineId; url: string }> = [
		{ id: 'A', url: window.location.origin },
	];
	// ── Dual-Engine State ──
	let isDualEngine = $state(false);
	let engineBDetected = false;
	let reconcilerInterval: ReturnType<typeof setInterval> | undefined;
	let videoFailoverActive = false;
	// Video is on Engine B because Engine A's program preview stalled. Sync
	// health says nothing about A's media, so the restore on 'synced' must
	// not undo this; a stall on B brings video back to A.
	let stallFailover = false;
	let failoverTimestamps: number[] = [];
	const FAILOVER_MAX_COUNT = 2;
	const FAILOVER_WINDOW_MS = 30_000;
	let audioPreWarmed = false;

	function canFailover(): boolean {
		const now = Date.now();
		failoverTimestamps = failoverTimestamps.filter(t => now - t < FAILOVER_WINDOW_MS);
		return failoverTimestamps.length < FAILOVER_MAX_COUNT;
	}

	function recordFailover(): void {
		failoverTimestamps.push(Date.now());
	}

	let initialLoadRetries = 0;

	// Speculatively enable command buffering for engine B discovery.
	// If engine B is never discovered (standalone mode), buffered commands
	// simply expire after 5s with no side effects.
	setExpectDualEngine(true);

	const store = createControlRoomStore();
	let showOverlay = $state(false);
	let statsPanelVisible = $state(false);
	let pipelineGraphVisible = $state(false);
	let ioPanelVisible = $state(false);
	let activeBottomTab = $state('Audio');
	let composeSubTab = $state<'dve' | 'graphics' | 'keys'>('dve');
	let layoutTabActive = $derived(activeBottomTab === 'Compose' && composeSubTab === 'dve');
	let graphicsTabActive = $derived(activeBottomTab === 'Compose' && composeSubTab === 'graphics');
	let graphicsSelectedLayerId = $state<number | null>(null);
	let layoutMode = $state<LayoutMode>(getLayoutMode());
	let mounted = $state(false);
	let connectionState = $state<'webtransport' | 'polling' | 'disconnected'>('disconnected');
	let initialLoading = $state(true);
	let connectionError: string | null = $state(null);
	let tokenRequired = $state(false);
	let tokenInput = $state('');
	let showOperatorRegistration = $state(false);
	let fastControlA = $state<FastControl | null>(null);
	let fastControlB = $state<FastControl | null>(null);
	let transportChangeCount = $state(0);
	let fastControl = $derived.by(() => {
		if (!fastControlA) return null;
		if (fastControlB) return createDualFastControl([fastControlA, fastControlB]);
		if (isDualEngine) return createDualFastControl([fastControlA]);
		return fastControlA;
	});
	// Debounced warning for FastControl write failures — at most once per second per message type.
	const _fcErrTimestamps = new Map<string, number>();
	function handleFastControlWriteError(msgType: string, engineId?: string) {
		const now = Date.now();
		const last = _fcErrTimestamps.get(msgType) ?? 0;
		if (now - last > 1000) {
			_fcErrTimestamps.set(msgType, now);
			console.warn(`[FastControl] datagram write failed: ${msgType} (engine ${engineId ?? 'unknown'})`);
		}
		// Close only the failed engine's writer, not both
		if (engineId === 'A' || !engineId) {
			if (fastControlA) {
				try { fastControlA.close(); } catch { /* already closed */ }
				fastControlA = null;
			}
		}
		if (engineId === 'B' || !engineId) {
			if (fastControlB) {
				try { fastControlB.close(); } catch { /* already closed */ }
				fastControlB = null;
			}
		}
	}

	let commsVisible = $state(false);
	let commsDimmed = $state(false);
	let redundancyPanelVisible = $state(false);
	let compareMode = $state(false);
	let activeEngine = $state<'A' | 'B'>('A');

	// Caption renderer for program monitor overlay
	let captionRenderer: CaptionRenderer | null = null;

	function onCaptionElReady(el: HTMLDivElement) {
		captionRenderer?.destroy();
		captionRenderer = new CaptionRenderer(el, undefined, false);
	}

	// ARIA live region for screen reader announcements
	let announcement = $state('');
	let announcementTimer: ReturnType<typeof setTimeout> | undefined;

	// Periodic tick for sync status detection (drives time-based re-evaluation)
	let now = $state(Date.now());
	let syncInterval: ReturnType<typeof setInterval> | undefined;
	// Forward declaration — initialized in onMount block below.
	let connectionManager: DualConnectionManager = undefined!;

	let syncStatus = $derived.by(() => {
		if (isDualEngine) {
			// In dual-engine mode, delegate to the reconciler's health status.
			const health = connectionManager.health;
			if (health.status === 'synced') return 'ok' as const;
			if (health.status === 'disconnected') return 'disconnected' as const;
			return 'resyncing' as const;
		}
		// Single-engine mode: when MoQ is delivering state via the media
		// pipeline (connectionState is 'webtransport'), trust it — state
		// updates are event-driven, so gaps during idle periods are normal.
		if (connectionState === 'webtransport') return 'ok' as const;
		// Fallback: time-based detection for polling mode.
		const elapsed = now - store.lastServerUpdate;
		if (elapsed > 5000) return 'disconnected' as const;
		if (elapsed > 2000) return 'resyncing' as const;
		return 'ok' as const;
	});

	// Dual-engine sync health — null in single-engine mode (child components use this)
	let syncHealth = $derived(isDualEngine ? connectionManager.health : null);

	// Per-engine health detail — null in single-engine mode (child components use this)
	let engineHealth = $derived(isDualEngine ? connectionManager.engineHealth : null);

	// Control plane session recovery URL — derived from hostname (e.g. abc123.switchframe.dev → abc123)
	const sessionFallbackUrl = $derived.by(() => {
		const parts = window.location.hostname.split('.');
		if (parts.length < 3) return null;
		const sessionId = parts[0].replace(/-b$/, '');
		return `https://control-plane.switchframe.dev/session/${sessionId}`;
	});

	// Which engine served this page — derived from our origin vs the known
	// engine URL list. Uses getEngineUrls() (static, set during discovery)
	// rather than store.state.engineBUrl (reactive, changes with authoritative
	// engine and could point to the dead engine).
	const servingEngine = $derived.by((): 'A' | 'B' => {
		if (!isDualEngine) return 'A';
		const engines = getEngineUrls();
		if (!engines || engines.length < 2) return 'A';
		const engineB = engines.find(e => e.id === 'B');
		if (!engineB) return 'A';
		try {
			if (new URL(engineB.url).origin === window.location.origin) return 'B';
		} catch { /* fall through */ }
		return 'A';
	});

	// Fallback URL for ConnectionBanner redirect — uses engine URL list
	// rather than store.state.engineBUrl which changes meaning with authority.
	const engineFallbackUrl = $derived.by((): string | null => {
		if (!isDualEngine) return null;
		const engines = getEngineUrls();
		if (!engines || engines.length < 2) return null;
		const otherId = servingEngine === 'A' ? 'B' : 'A';
		return engines.find(e => e.id === otherId)?.url ?? null;
	});

	// Per-engine connection state for ConnectionBanner indicators.
	// Keyed on transportChangeCount so Svelte re-derives when any engine's state changes.
	let perEngineState = $derived.by(() => {
		void transportChangeCount; // reactive dependency
		if (!isDualEngine) return undefined;
		return {
			A: connectionManager.engineConnectionState('A'),
			B: connectionManager.engineConnectionState('B'),
		};
	});

	// Per-source dual health map for IOPanel (combines A + B source health into single record)
	let ioSourceHealth = $derived.by(() => {
		if (!engineHealth) return null;
		const result: Record<string, { A: import('$lib/api/types').SourceHealthStatus | 'not_present'; B: import('$lib/api/types').SourceHealthStatus | 'not_present' }> = {};
		const allKeys = new Set([...Object.keys(engineHealth.A.sources), ...Object.keys(engineHealth.B.sources)]);
		for (const key of allKeys) {
			result[key] = {
				A: engineHealth.A.sources[key] ?? 'not_present',
				B: engineHealth.B.sources[key] ?? 'not_present',
			};
		}
		return result;
	});

	// SRT endpoint URLs for dual-engine info blurb
	let ioEngineEndpoints = $derived.by(() => {
		if (!isDualEngine) return null;
		const stateA = store.effectiveState;
		const ciA = stateA.connectionInfo;
		if (!ciA) return null;
		// Engine A URL is known from current connection; Engine B from engineBUrl
		const portA = ciA.srtIngestPort ?? 6464;
		const hostA = ciA.domain ?? window.location.hostname;
		const engineBUrl = stateA.engineBUrl;
		if (!engineBUrl) return null;
		try {
			const urlB = new URL(engineBUrl);
			const hostB = urlB.hostname;
			return { A: `srt://${hostA}:${portA}`, B: `srt://${hostB}:${portA}` };
		} catch {
			return null;
		}
	});

	// Divergence log for dual-engine mode
	const divergenceLog = createDivergenceLog();

	// Auto-reconcile: watches sync status and triggers snapshot push when engines diverge
	const autoReconcile = createAutoReconcile({
		getSnapshot: (engine, lockId) => dualApi.getStateSnapshot(engine, lockId),
		pushSnapshot: (engine, snapshot) => dualApi.pushStateSnapshot(engine, snapshot),
		acquireLock: (engine) => dualApi.acquireReconcileLock(engine),
		releaseLock: (engine, lockId) => dualApi.releaseReconcileLock(engine, lockId),
		getAuthoritativeState: () => {
			const s = connectionManager.state;
			return s ? { inTransition: s.inTransition } : null;
		},
		checkCriticalDivergence: () => {
			return connectionManager.health.status === 'mismatch';
		},
		checkAnyDivergence: () => {
			return (connectionManager.health.divergences?.length ?? 0) > 0;
		},
		onRecoveryStart: () => {
			connectionManager.setRecovering(true);
			divergenceLog.add({ type: 'snapshot', message: 'Snapshot push started...' });
		},
		onRecoveryComplete: (warnings) => {
			connectionManager.setRecovering(false);
			divergenceLog.add({ type: 'resolved', message: 'Sync restored', warnings });
		},
		onRecoveryError: (error) => {
			connectionManager.setRecovering(false);
			divergenceLog.add({ type: 'diverged', message: `Recovery failed: ${error.message}` });
		},
		onStaleConflict: (target, snapshotSeq, currentSeq) => {
			connectionManager.setRecovering(false);
			const msg = `Multi-region sync degraded — manually reconcile or restart engine (${target}: snapshot seq ${snapshotSeq} < engine seq ${currentSeq})`;
			divergenceLog.add({ type: 'diverged', message: msg });
			notify('error', msg);
		},
		onReconcileBusy: (engine) => {
			divergenceLog.add({
				type: 'snapshot',
				message: `peer browser holds reconcile lock on engine ${engine} — backing off`,
			});
		},
	});

	// Dual-engine connection banner status.
	// The design doc distinguishes "unreachable" (browser can't reach engine, but it may
	// still be running) from "offline" (engine actually down, detected by control plane).
	// Currently we always report "unreachable" since the browser has no way to query the
	// control plane for engine liveness. This will be improved when the control plane
	// exposes a session health WebSocket or similar push mechanism.
	const engineBannerStatus = $derived.by(() => {
		if (!isDualEngine) return undefined;
		const eA = engineHealth?.A;
		const eB = engineHealth?.B;
		if (!eA?.connected && !eB?.connected) return { mode: 'dual' as const };
		if (!eB?.connected) return { mode: 'dual' as const, engineDown: 'B' as const, reason: 'unreachable' as const };
		if (!eA?.connected) return { mode: 'dual' as const, engineDown: 'A' as const, reason: 'unreachable' as const };
		return { mode: 'dual' as const };
	});

	// Audio drift between engines (compares configuration, not transient peaks)
	const audioDrift = $derived.by(() => {
		if (!isDualEngine) return null;
		const rawA = connectionManager.rawStates?.A;
		const rawB = connectionManager.rawStates?.B;
		if (!rawA?.audioChannels || !rawB?.audioChannels) return null;
		return detectAudioConfigDrift(rawA.audioChannels, rawB.audioChannels);
	});

	// Engine B state for OutputControls (raw state from reconciler)
	const engineBState = $derived.by(() => {
		if (!isDualEngine) return null;
		return connectionManager.rawStates?.B ?? null;
	});

	// Engine destinations for IOPanel (per-destination status from both engines)
	const ioEngineDestinations = $derived.by(() => {
		if (!isDualEngine) return null;
		const rawA = connectionManager.rawStates?.A;
		const rawB = connectionManager.rawStates?.B;

		// Need both states to have destinations arrays to show comparison
		if (!rawA?.destinations && !rawB?.destinations) return null;
		if (!rawA?.destinations || !rawB?.destinations) return null;

		const result: Record<string, { A: { state: string; bitrate?: number }; B: { state: string; bitrate?: number } }> = {};

		// Index Engine B destinations by ID for O(1) lookup
		const destBMap = new Map<string, (typeof rawB.destinations)[0]>();
		for (const d of rawB.destinations) {
			destBMap.set(d.id, d);
		}

		// Add all Engine A destinations
		for (const destA of rawA.destinations) {
			const destB = destBMap.get(destA.id);
			result[destA.id] = {
				A: { state: destA.state },
				B: destB ? { state: destB.state } : { state: 'not configured' },
			};
			destBMap.delete(destA.id);
		}

		// Add Engine B-only destinations
		for (const [id, destB] of destBMap) {
			result[id] = {
				A: { state: 'not configured' },
				B: { state: destB.state },
			};
		}

		return Object.keys(result).length > 0 ? result : null;
	});

	// Playout comparison for RedundancyPanel (compares channel 0 across engines)
	const playoutComparison = $derived.by(() => {
		if (!isDualEngine) return null;
		const stateA = connectionManager.rawStates?.A;
		const stateB = connectionManager.rawStates?.B;
		if (!stateA?.playout?.channels?.[0] || !stateB?.playout?.channels?.[0]) return null;

		const chA = stateA.playout.channels[0];
		const chB = stateB.playout.channels[0];

		// Only show when at least one engine is actively playing
		if (chA.state === 'stopped' && chB.state === 'stopped') return null;

		return {
			itemA: chA.currentTitle ?? `Item ${chA.itemIndex}`,
			itemB: chB.currentTitle ?? `Item ${chB.itemIndex}`,
			inSync: chA.itemIndex === chB.itemIndex && chA.currentTitle === chB.currentTitle,
			ptsFrameDelta: undefined as number | undefined,
			cacheA: undefined as string | undefined,
			cacheB: undefined as string | undefined,
		};
	});

	// Audio comparison for RedundancyPanel (per-channel peak level delta)
	const audioComparison = $derived.by(() => {
		if (!isDualEngine || !connectionManager) return null;
		const stateA = connectionManager.rawStates?.A;
		const stateB = connectionManager.rawStates?.B;
		if (!stateA?.audioChannels || !stateB?.audioChannels) return null;

		const result: { channel: string; levelA: number; levelB: number; withinTolerance: boolean }[] = [];
		for (const [key, chA] of Object.entries(stateA.audioChannels)) {
			const chB = stateB.audioChannels[key];
			if (!chB) continue;
			const delta = Math.abs(chA.peakL - chB.peakL);
			result.push({
				channel: key,
				levelA: chA.peakL,
				levelB: chB.peakL,
				withinTolerance: delta <= 3,
			});
		}
		return result.length > 0 ? result : null;
	});

	// Output status for RedundancyPanel (recording + destination state per engine)
	const outputStatus = $derived.by(() => {
		if (!isDualEngine || !connectionManager) return null;
		const stateA = connectionManager.rawStates?.A;
		const stateB = connectionManager.rawStates?.B;

		const recording = {
			A: stateA?.recording?.active ?? false,
			B: stateB?.recording?.active ?? false,
		};

		const destinations: { name: string; A: { state: string; bitrate?: number }; B: { state: string; bitrate?: number } }[] = [];
		if (stateA?.destinations) {
			for (const destA of stateA.destinations) {
				const destB = stateB?.destinations?.find((d: { id: string }) => d.id === destA.id);
				destinations.push({
					name: destA.name ?? destA.id,
					A: { state: destA.state },
					B: destB ? { state: destB.state } : { state: 'unknown' },
				});
			}
		}

		return { recording, destinations };
	});

	// Force reconcile handler for RedundancyPanel
	async function handleForceReconcile(direction: 'AtoB' | 'BtoA') {
		const from = direction === 'AtoB' ? 'A' : 'B';
		const to = direction === 'AtoB' ? 'B' : 'A';
		try {
			const snapshot = await dualApi.getStateSnapshot(from);
			const result = await dualApi.pushStateSnapshot(to, snapshot);
			divergenceLog.add({ type: 'snapshot', message: `Force reconcile ${direction} complete`, warnings: result.warnings });
			notify('info', 'Force reconcile complete');
		} catch (e) {
			notify('error', `Force reconcile failed: ${e}`);
		}
	}

	// Compare mode handler for RedundancyPanel
	function handleCompare() {
		compareMode = true;
		redundancyPanelVisible = false;
	}

	function announce(msg: string) {
		announcement = msg;
		clearTimeout(announcementTimer);
		announcementTimer = setTimeout(() => { announcement = ''; }, 3000);
	}

	// Canvas refs passed up from ProgramPreview / SimpleMode via onCanvasReady
	let programCanvas: HTMLCanvasElement | null = $state(null);
	let previewCanvas: HTMLCanvasElement | null = $state(null);

	function onCanvasReady(preview: HTMLCanvasElement, program: HTMLCanvasElement) {
		previewCanvas = preview;
		programCanvas = program;
	}

	function switchLayout() {
		// Detach all canvases BEFORE the layout state change. This must run
		// synchronously so it happens before Svelte destroys the old component
		// and mounts the new one. If deferred to an $effect, it races with the
		// child component's onCanvasReady $effect — the child may provide new
		// canvas refs that the layout-change effect then immediately detaches.
		pipelineManager.onLayoutChange();
		programCanvas = null;
		previewCanvas = null;
		if (layoutMode === 'traditional') layoutMode = 'simple';
		else if (layoutMode === 'simple') layoutMode = 'on-air';
		else layoutMode = 'traditional';
		setLayoutMode(layoutMode);
	}
	let transitionType = $state<string>('mix');
	let transitionDuration = $state(1000);
	let selectedAudioChannel = $state<string | null>(null);

	const keyboard = new KeyboardHandler({
		onCut: () => cutToPreview(store),
		onSetPreview: (key) => {
			store.optimisticPreview(key);
			apiCall(setPreview(key), 'Preview failed');
		},
		onHotPunch: (key) => {
			store.optimisticCut(key);
			apiCall(cut(key), 'Cut failed');
		},
		onAutoTransition: () => autoTransitionToPreview(store, transitionType, transitionDuration),
		onFadeToBlack: () => {
			if (!store.state.inTransition || store.state.ftbActive) {
				apiCall(fadeToBlack(), 'FTB failed');
			}
		},
		onToggleFullscreen: () => {
			document.fullscreenElement
				? document.exitFullscreen()
				: document.documentElement.requestFullscreen();
		},
		onToggleOverlay: () => { showOverlay = !showOverlay; },
		onToggleDSK: () => {
			const gfxLayers = store.state.graphics?.layers ?? [];
			if (gfxLayers.some((l) => l.active)) {
				// Turn off all active layers.
				const offs = gfxLayers.filter((l) => l.active).map((l) => graphicsOff(l.id));
				apiCall(Promise.all(offs).then((r) => r[0]), 'Graphics failed');
			} else if (gfxLayers.length > 0) {
				// Turn on the first layer.
				apiCall(graphicsOn(gfxLayers[0].id), 'Graphics failed');
			}
		},
		onSetTransitionType: (type) => {
			if (type === 'mix' || type === 'dip') {
				transitionType = type;
			}
		},
		onRunMacro: (slotIndex) => {
			if (slotIndex < macroList.length) {
				apiCall(runMacro(macroList[slotIndex].name), 'Macro failed');
			}
		},
		layoutTogglePIP: () => {
			const slots = store.state.layout?.slots;
			if (slots && slots.length > 0) {
				const slot = slots[0];
				apiCall(slot.enabled ? dveSlotOff(0) : dveSlotOn(0), 'PIP toggle');
			}
		},
		isReplayTabActive: () => activeBottomTab === 'Replay',
		replayQuickPreset: (index: number) => {
			const presets = [
				{ seconds: 15 }, { seconds: 30 }, { seconds: 60 }
			];
			try {
				const saved = localStorage.getItem('switchframe:replay-presets');
				if (saved) {
					const parsed = JSON.parse(saved);
					if (index < parsed.length) {
						apiCall(replayQuick(parsed[index].seconds), 'Quick replay');
						return;
					}
				}
			} catch { /* ignore */ }
			if (index < presets.length) {
				apiCall(replayQuick(presets[index].seconds), 'Quick replay');
			}
		},
		replayMarkIn: () => {
			const src = store.effectiveState.programSource || Object.keys(store.effectiveState.sources)[0] || '';
			if (src) apiCall(replayMarkIn(src), 'Mark IN');
		},
		replayMarkOut: () => {
			const src = store.effectiveState.replay?.markSource || '';
			if (src) apiCall(replayMarkOut(src), 'Mark OUT');
		},
		replayPlayPause: () => {
			const r = store.effectiveState.replay;
			if (r?.state === 'playing') {
				apiCall(replayPause(), 'Pause');
			} else if (r?.state === 'paused') {
				apiCall(replayResume(), 'Resume');
			} else if (r?.markIn && r?.markOut && r?.markSource) {
				apiCall(replayPlay(r.markSource, 0.5, false), 'Play');
			}
		},
		replayStop: () => {
			apiCall(replayStop(), 'Stop replay');
		},
		replaySpeedDown: () => {
			const speeds = [0.25, 0.5, 0.75, 1.0];
			const current = store.effectiveState.replay?.speed ?? 1.0;
			const idx = speeds.indexOf(current);
			if (idx > 0) apiCall(replaySetSpeed(speeds[idx - 1]), 'Speed down');
		},
		replaySpeedUp: () => {
			const speeds = [0.25, 0.5, 0.75, 1.0];
			const current = store.effectiveState.replay?.speed ?? 1.0;
			const idx = speeds.indexOf(current);
			if (idx < speeds.length - 1) apiCall(replaySetSpeed(speeds[idx + 1]), 'Speed up');
		},
		replayFrameBack: () => {
			const pos = Math.max(0, (store.effectiveState.replay?.position ?? 0) - 0.002);
			apiCall(replaySeek(pos), 'Frame back');
		},
		replayFrameForward: () => {
			const pos = Math.min(1, (store.effectiveState.replay?.position ?? 0) + 0.002);
			apiCall(replaySeek(pos), 'Frame forward');
		},
		commsToggleMute: () => {
			const comms = store.effectiveState.comms;
			if (comms?.active) {
				const session = operatorState.getSession();
				if (session) {
					const self = comms.participants.find(p => p.operatorId === session.id);
					if (self) {
						commsMute(session.id, !self.muted);
					}
				}
			}
		},
		getSourceKeys: () => store.sourceKeys,
	});

	// Speculatively discover Engine B in parallel with the initial state fetch.
	// This is assigned after the connectionManager constructor but the closure
	// inside onInitialLoadError captures it by reference, so the timing works.
	let speculativeEngineB: Promise<{ engineBUrl: string; source: string } | null> | null = null;

	// URL is the page origin — in production (embedded UI on :8080), WebTransport
	// connects same-origin. In dev (Vite :5173), the connection.ts WebTransport
	// path will fail (Vite doesn't speak QUIC), which is fine: MoQ state arrives
	// via the media pipeline's per-source MoQTransport instead.
	// DualConnectionManager handles both single-engine and dual-engine modes.
	function handleStateUpdate(update: ControlRoomState) {
		if (isDualEngine) {
			// In dual mode, the reconciler already selected the authoritative
			// state — bypass seq checking since the new authoritative engine
			// may have a lower seq than the previous one.
			store.forceUpdate(update);
		} else {
			store.applyUpdate(update);
		}
		// Auto-detect engine B from session metadata (once only).
		if (!engineBDetected) {
			const engineBUrl = store.state.engineBUrl;
			if (engineBUrl) {
				engineBDetected = true;
				isDualEngine = true;
				wireEngineBWhenDiscovered(engineBUrl);
				// Start warm standby immediately — don't wait for mounted $effect.
				// This closes the 200-500ms gap between engine B detection and mount
				// during which a failover would have no warm pipeline ready.
				pipelineManager.enableWarmStandby(engineBUrl, Object.keys(store.state.sources ?? {}), {
					onControlState: (data) => connectionManager.handleControlData('B', data),
				});
			}
		}
	}

	function handleConnectionStateChange(state: 'webtransport' | 'polling' | 'disconnected') {
		connectionState = state;
		transportChangeCount++;
		if (state !== 'disconnected') {
			try { sessionStorage.removeItem('sf:redirect-attempts'); } catch {}
		}
	}

	function handleInitialLoadError(error: string, rawError?: unknown) {
		initialLoadRetries++;

		// Use the speculative Engine B discovery that was started in parallel
		// with the initial state fetch. On first error, consume the result
		// immediately — no need to start a new discovery round.
		if (!engineBDetected && speculativeEngineB) {
			speculativeEngineB.then(result => {
				if (!engineBDetected && result) {
					engineBDetected = true;
					isDualEngine = true;
					wireEngineBWhenDiscovered(result.engineBUrl);
				}
			}).catch(() => { /* Speculative discovery failed — standalone mode */ });
		}

		console.warn('Failed to fetch initial state:', error);
		if (rawError instanceof SwitchApiError && rawError.status === 401) {
			tokenRequired = true;
			initialLoading = false;
		}
		connectionError = error;
	}

	connectionManager = new DualConnectionManager({
		engines: engineConfig,
		onStateUpdate: handleStateUpdate,
		onConnectionStateChange: handleConnectionStateChange,
		onInitialLoadComplete: () => {
			initialLoading = false;
			connectionError = null;
		},
		onInitialLoadError: handleInitialLoadError,
	});

	// Kick off speculative Engine B discovery in parallel with the initial state
	// fetch. If Engine A responds with engineBUrl in state, that takes priority
	// (see onStateUpdate). If Engine A dies before responding, onInitialLoadError
	// consumes this promise so the fallback URL is already available.
	if (!engineBDetected) {
		const discovery = new ControlPlaneDiscovery(window.location.hostname);
		const inferred = inferEngineBUrl(window.location.hostname, window.location.protocol);
		const candidates: Promise<{ engineBUrl: string; source: string }>[] = [];
		if (discovery.sessionId) {
			candidates.push(
				discovery.fetchEngineUrls().then(r =>
					r ? { engineBUrl: r.engineBUrl, source: 'control-plane' } : Promise.reject(new Error('no result'))
				)
			);
		}
		if (inferred) {
			candidates.push(Promise.resolve({ engineBUrl: inferred, source: 'dns-inference' }));
		}
		if (candidates.length > 0) {
			speculativeEngineB = Promise.any(candidates).catch(() => null);
			// Set emergency fallback URL as soon as any candidate resolves,
			// even before full dual-engine wiring. This allows API calls to
			// fall back to Engine B if Engine A dies during initial load.
			speculativeEngineB.then(result => {
				if (result && !engineBDetected) {
					setEmergencyEngineBUrl(result.engineBUrl);
				}
			}).catch(() => {});
		}
	}

	const dualApi = new DualApiClient({
		engines: engineConfig,
	});

	/** Wire Engine B into all subsystems once its URL is known. */
	function wireEngineBWhenDiscovered(engineBUrl: string) {
		connectionManager.addEngine('B', engineBUrl);
		dualApi.addEngine('B', engineBUrl);
		setEngineUrls([
			{ id: 'A', url: window.location.origin },
			{ id: 'B', url: engineBUrl },
		]);
		setDualTimestampProvider((engineId?: string) => {
			if (!connectionManager.clock.isConverged()) {
				return { execute_at: 0, command_seq: connectionManager.nextCommandSeq() };
			}
			return {
				execute_at: engineId
					? connectionManager.clock.ptpNowForEngine(engineId as any) + connectionManager.clock.safetyMargin()
					: connectionManager.clock.ptpNow() + connectionManager.clock.safetyMargin(),
				command_seq: connectionManager.nextCommandSeq(),
			};
		});
		setPartialFailureCallback(() => {
			connectionManager.requestCheck();
		});
		setPartialFailureNotify((msg) => {
			notify('warning', msg);
		});
		setAuthoritativeEngineProvider(() => connectionManager.health.authoritative);
		setEmergencyEngineBUrl(engineBUrl);
		setNonIdempotentFailureCallback((url, failedEngines) => {
			divergenceLog.add({ type: 'mismatch', message: `Non-idempotent ${url} failed on ${failedEngines.join(', ')}` });
		});
		if (!reconcilerInterval) {
			reconcilerInterval = setInterval(() => { connectionManager.tick(); }, 200);
		}
	}

	// Media pipeline for MoQ video/audio decode
	const pipeline = createMediaPipeline({
		onControlState: (data) => {
			// Media pipeline currently connects to engine A only
			connectionManager.handleControlData('A', data);
		},
		onMoQActive: () => {
			connectionManager.notifyMoQActive();
		},
		onProgramCaptionFrame: (caption, _timestamp) => {
			captionRenderer?.show(caption);
		},
		onSourceStalled: (key) => {
			// Only trigger failover for program sources — tile stalls don't warrant
			// switching the entire media pipeline to another engine.
			if (key !== 'program-preview') return;
			if (!isDualEngine) return;
			if (videoFailoverActive) return;

			if (pipelineManager.activeEngine === 'A') {
				if (!store.state.engineBUrl) {
					// Attempt emergency engine B discovery via DNS inference
					const inferred = inferEngineBUrl(window.location.hostname, window.location.protocol);
					if (inferred && !engineBDetected) {
						engineBDetected = true;
						isDualEngine = true;
						wireEngineBWhenDiscovered(inferred);
						// Try failover after brief delay for connection setup
						setTimeout(() => {
							if (!canFailover()) {
								console.warn('[failover] Circuit breaker: max failovers reached in window');
								notify('warning', 'Video failover suppressed — both engines may be degraded');
								return;
							}
							recordFailover();
							videoFailoverActive = true;
							stallFailover = true;
							pipelineManager.failoverToEngineB(inferred, Object.keys(store.state.sources ?? {}))
								.then(() => {
									activeEngine = 'B';
									videoFailoverActive = false;
									notify('warning', 'Video switched to Engine B — click to resume audio');
									requestAudioResumeAfterFailover();
								})
								.catch(err => {
									videoFailoverActive = false;
									stallFailover = false;
									console.warn('Emergency failover failed:', err);
									notify('error', 'Video failover to Engine B failed');
								});
						}, 500);
					}
					return;
				}

				if (!canFailover()) {
					console.warn('[failover] Circuit breaker: max failovers reached in window');
					notify('warning', 'Video failover suppressed — both engines may be degraded');
					return;
				}
				recordFailover();
				videoFailoverActive = true;
				stallFailover = true;
				pipelineManager.failoverToEngineB(store.state.engineBUrl, Object.keys(store.state.sources ?? {}))
					.then(() => {
						activeEngine = 'B';
						videoFailoverActive = false;
						notify('warning', 'Video switched to Engine B — click to resume audio');
						requestAudioResumeAfterFailover();
					})
					.catch(err => {
						videoFailoverActive = false;
						stallFailover = false;
						console.warn('Media stall failover failed:', err);
						notify('error', 'Video failover to Engine B failed');
					});
			} else if (pipelineManager.activeEngine === 'B') {
				// Engine B is stalling — try restoring to Engine A if reconnected.
				// Restoring to Engine A is safe — do NOT consume circuit breaker budget.
				if (connectionManager.engineConnectionState('A') !== 'disconnected') {
					videoFailoverActive = true;
					pipelineManager.restoreToEngineA(Object.keys(store.state.sources ?? {}))
						.then(() => {
							activeEngine = 'A';
							videoFailoverActive = false;
							stallFailover = false;
							notify('warning', 'Video restored to Engine A — click to resume audio');
							requestAudioResumeAfterFailover();
						})
						.catch(err => {
							videoFailoverActive = false;
							console.warn('Restore to A after B stall failed:', err);
							notify('error', 'Video restore to Engine A failed');
						});
				}
			}
		},
	});
	// Pre-register the program-preview source so ProgramPreview's $effect can
	// attach the canvas renderer before onMount (which connects the MoQ
	// transport). The UI renders exclusively from program-preview (low-bitrate,
	// ~3 Mbps); full-quality `program` is reserved for SRT / file output.
	pipeline.setSourceMuted('program-preview', false);
	pipeline.addSource('program-preview');

	const pipelineManager = new PipelineManager(pipeline, () => store.sourceKeys, (src, pgm) => {
		sourceLevels = src;
		programLevels = pgm;
	});

	// PFL/AFL (Pre/After-Fade Listen) manager for client-side per-source audio monitoring
	const pflManager = createPFLManager();
	let pflActiveSource = $state<string | null>(null);
	let pflActiveMode = $state<import('$lib/audio/pfl').PFLMode>('off');
	const pflToggle = createPFLToggle({ pflManager, pipeline });

	// Macro list for keyboard trigger (Ctrl+1-9)
	let macroList = $state<Macro[]>([]);
	async function refreshMacros() {
		try { macroList = await listMacros(); } catch { /* ignore */ }
	}

	function handleLabelChange(key: string, label: string) {
		apiCall(setLabel(key, label), 'Label update failed');
	}

	function handlePFLToggle(sourceKey: string) {
		const result = pflToggle.toggle(sourceKey);
		pflActiveSource = result.source;
		pflActiveMode = result.mode;
	}

	// Per-source audio levels sampled from media pipeline decoders (linear 0..1)
	let sourceLevels = $state<Record<string, { peakL: number; peakR: number }>>({});
	// Program output peak levels sampled from program audio decoder (linear 0..1)
	let programLevels = $state<{ peakL: number; peakR: number }>({ peakL: 0, peakR: 0 });

	function handleGlobalShortcuts(e: KeyboardEvent) {
		// Single focus guard for all page-level shortcuts: when focus is in a text-editing
		// context (INPUT/TEXTAREA/SELECT/contenteditable) none of these should fire — this
		// includes Ctrl+Shift+D and the Escape panel handlers, so Escape in a field can't
		// unexpectedly close panels. Mirrors KeyboardHandler's capture-phase guard.
		if (isEditableTarget(e.target)) return;

		if (e.ctrlKey && e.shiftKey && (e.key === 'd' || e.key === 'D')) {
			e.preventDefault();
			exportDebugSnapshot();
		}
		// Shift+G toggles pipeline graph
		if (e.shiftKey && !e.ctrlKey && !e.metaKey && e.code === 'KeyG') {
			e.preventDefault();
			pipelineGraphVisible = !pipelineGraphVisible;
			return;
		}
		// Shift+P toggles stats panel
		if (e.shiftKey && !e.ctrlKey && !e.metaKey && e.code === 'KeyP') {
			e.preventDefault();
			statsPanelVisible = !statsPanelVisible;
		}
		// Shift+I toggles I/O panel
		if (e.shiftKey && !e.ctrlKey && !e.metaKey && e.code === 'KeyI') {
			e.preventDefault();
			ioPanelVisible = !ioPanelVisible;
		}
		// Escape closes compare mode
		if (e.code === 'Escape' && compareMode) {
			e.preventDefault();
			compareMode = false;
			return;
		}
		// Escape closes redundancy panel
		if (e.code === 'Escape' && redundancyPanelVisible) {
			e.preventDefault();
			redundancyPanelVisible = false;
			return;
		}
		// Escape closes pipeline graph (higher priority than stats panel)
		if (e.code === 'Escape' && pipelineGraphVisible) {
			e.preventDefault();
			pipelineGraphVisible = false;
			return;
		}
		// Escape closes stats panel
		if (e.code === 'Escape' && statsPanelVisible) {
			e.preventDefault();
			statsPanelVisible = false;
		}
	}

	async function exportDebugSnapshot() {
		const frontend = { sources: await pipeline.getAllDiagnostics() };

		let backend: Record<string, unknown> | null = null;
		try {
			const [debug, perf] = await Promise.all([
				getDebugSnapshot().catch(() => null),
				getPerfData().catch(() => null),
			]);
			if (debug || perf) {
				backend = { ...(debug ?? {}), perf };
			}
		} catch { /* ignore */ }

		const snapshot: Record<string, unknown> = {
			timestamp: new Date().toISOString(),
			frontend,
			backend,
		};

		if (isDualEngine) {
			snapshot.dualEngine = {
				reconciler: connectionManager.dualEngineStats,
				autoReconcile: autoReconcile.stats,
				videoFailoverCount: failoverTimestamps.length,
				engineA: {
					state: engineHealth?.A?.connected ? 'connected' : 'disconnected',
					rtt: engineHealth?.A?.rtt ?? 0,
				},
				engineB: {
					state: engineHealth?.B?.connected ? 'connected' : 'disconnected',
					rtt: engineHealth?.B?.rtt ?? 0,
				},
			};
		}

		const json = JSON.stringify(snapshot, null, 2);
		try {
			await navigator.clipboard.writeText(json);
			notify('info', 'Debug snapshot copied to clipboard');
		} catch {
			console.log('=== SWITCHFRAME DEBUG SNAPSHOT ===');
			console.log(json);
			notify('info', 'Debug snapshot logged to console');
		}
	}

	// React to source list changes — delegate to PipelineManager
	$effect(() => {
		// Access sourceKeys to create the reactive dependency
		const _keys = store.sourceKeys;
		if (!mounted) return;
		tick().then(() => {
			pipelineManager.syncSources(store.state.sources);
			// In compare mode ProgramCompare owns the program canvas.
			if (compareMode) return;
			pipelineManager.syncProgramPreviewCanvases(store.effectiveState.previewSource, programCanvas, previewCanvas);
		});
	});

	// React to program/preview changes and canvas ref updates
	let prevProgramSource: string | undefined;
	$effect(() => {
		const _program = store.state.programSource;
		// Use effectiveState for preview so canvas switches instantly on user
		// click (optimistic update) instead of waiting for MoQ round-trip.
		const _preview = store.effectiveState.previewSource;
		const _pgmCanvas = programCanvas;
		const _pvwCanvas = previewCanvas;
		// In compare mode ProgramCompare owns the program canvas; the
		// ProgramPreview canvas held here is unmounted, and attaching it again
		// would take the program renderer away from the compare canvas.
		if (!mounted || compareMode) return;

		// Reset A/V sync tracking on program renderer when source changes
		// (transition completed). Prevents stale PTS from old source causing
		// transient sync swings with the new source's PTS.
		if (prevProgramSource !== undefined && _program !== prevProgramSource) {
			pipelineManager.notifyProgramSourceChange();
		}
		prevProgramSource = _program;

		pipelineManager.syncProgramPreviewCanvases(_preview, programCanvas, previewCanvas);
	});

	// Re-attach tile canvases when layout mode changes (DOM is replaced).
	// Program/preview canvases are handled by onCanvasReady → effect 324.
	// onLayoutChange() and canvas nulling are done synchronously in
	// switchLayout() to avoid racing with the child's onCanvasReady $effect.
	let prevLayoutMode: LayoutMode | undefined;
	$effect(() => {
		const mode = layoutMode;
		if (prevLayoutMode !== undefined && mode !== prevLayoutMode) {
			// Re-sync tile canvases after DOM updates (tile elements replaced).
			tick().then(() => {
				if (!mounted) return;
				pipelineManager.syncSources(store.state.sources);
			});
		}
		prevLayoutMode = mode;
	});

	// Sync PFL manager sources with pipeline sources
	$effect(() => {
		const keys = store.sourceKeys;
		for (const key of keys) {
			const decoder = pipeline.getAudioDecoder(key);
			if (decoder && !pflManager.getDecoder(key)) {
				pflManager.addSource(key, 'mp4a.40.2', 48000, 2);
			}
		}
	});

	// Track previous values for state change announcements
	let prevRecording: boolean | undefined;
	let prevFtb: boolean | undefined;

	$effect(() => {
		const isRecording = store.state.recording?.active === true;
		const isFtb = store.state.ftbActive;

		if (prevRecording !== undefined && isRecording !== prevRecording) {
			announce(isRecording ? 'Recording started' : 'Recording stopped');
		}
		if (prevFtb !== undefined && isFtb !== prevFtb && isFtb) {
			announce('Fade to black active');
		}

		prevRecording = isRecording;
		prevFtb = isFtb;
	});

	// Notify operator when the program source has a decoder error
	let prevProgramError: string | null = null;
	$effect(() => {
		const programKey = store.state.programSource;
		const error = programKey ? getSourceError(programKey) : null;
		if (error && error !== prevProgramError) {
			notify('error', `Program source error: ${error}`);
		}
		prevProgramError = error;
	});

	// Track previous sync status to only log on transitions (not every $effect fire)
	let prevSyncStatus: string | null = null;

	// Track sustained audio drift between engines — trigger reconciliation after 10s
	let audioDriftStart: number | null = null;
	let audioDriftInterval: ReturnType<typeof setInterval> | undefined;

	// Auto-reconcile: react to sync health status transitions
	$effect(() => {
		if (syncHealth && isDualEngine) {
			if (syncHealth.status !== prevSyncStatus) {
				const oldStatus = prevSyncStatus;
				prevSyncStatus = syncHealth.status;
				autoReconcile.onSyncStatusChange(syncHealth.status, syncHealth.authoritative);
				if (syncHealth.status === 'lagging') {
					divergenceLog.add({ type: 'lagging', message: 'Engine lagging behind' });
				} else if (syncHealth.status === 'diverged') {
					divergenceLog.add({ type: 'diverged', message: 'Engines diverged' });
				} else if (syncHealth.status === 'mismatch') {
					divergenceLog.add({ type: 'mismatch', message: 'Critical: program source mismatch' });
				} else if (syncHealth.status === 'synced' && oldStatus !== null) {
					divergenceLog.add({ type: 'resolved', message: 'Engines synced' });
				}
			}
		}
	});

	// Audio drift reconciliation: trigger auto-reconcile after 10s of sustained drift.
	// Uses setInterval so the threshold fires even if the drift value stabilizes.
	$effect(() => {
		if (audioDrift && isDualEngine) {
			if (!audioDriftStart) {
				audioDriftStart = Date.now();
			}
			if (!audioDriftInterval) {
				audioDriftInterval = setInterval(() => {
					if (audioDriftStart && Date.now() - audioDriftStart > 10_000) {
						autoReconcile.onSyncStatusChange('diverged', syncHealth?.authoritative ?? 'A');
						divergenceLog.add({ type: 'diverged', message: `Audio config drift on ${audioDrift?.channel} for >10s` });
						audioDriftStart = null;
						clearInterval(audioDriftInterval);
						audioDriftInterval = undefined;
					}
				}, 1000);
			}
		} else {
			audioDriftStart = null;
			if (audioDriftInterval) {
				clearInterval(audioDriftInterval);
				audioDriftInterval = undefined;
			}
		}
		return () => {
			if (audioDriftInterval) {
				clearInterval(audioDriftInterval);
				audioDriftInterval = undefined;
			}
		};
	});

	// Enable warm standby when dual-engine mode is detected: proactively create
	// Engine B's media pipeline and connect all sources (no canvases). This way
	// failover only needs to swap canvases instead of creating the pipeline and
	// waiting up to 3s for MoQ readiness.
	//
	// Also reacts to Engine B URL changes — if the control plane re-provisions
	// Engine B at a new URL (after crash), enableWarmStandby's underlying
	// createEngineBPipeline detects the URL change and rebuilds the pipeline
	// against the new URL. Identical URLs fast-path to no-op.
	$effect(() => {
		if (isDualEngine && store.state.engineBUrl && mounted) {
			const sourceKeys = Object.keys(store.state.sources ?? {});
			pipelineManager.enableWarmStandby(store.state.engineBUrl, sourceKeys, {
				onControlState: (data) => connectionManager.handleControlData('B', data),
			});
		}
	});

	// Sync warm standby sources when sources change (add new cameras, etc.)
	$effect(() => {
		if (isDualEngine && pipelineManager.hasEngineBPipeline && pipelineManager.activeEngine === 'A') {
			pipelineManager.syncWarmStandby(Object.keys(store.state.sources ?? {}));
		}
	});

	// Track warm standby health — surface a warning when Engine B's pipeline is
	// active but not receiving media (e.g. MoQ subscription dropped silently).
	$effect(() => {
		if (!isDualEngine || !mounted) return;
		const degraded = pipelineManager.warmStandbyActive && !pipelineManager.warmStandbyHealthy;
		connectionManager.setWarmStandbyDegraded(degraded);
	});

	// Clean up warm standby when Engine B disappears for good.
	// If the reconciler status is 'single-engine' and Engine B has been
	// disconnected for >30s, tear down Engine B pipeline to reclaim resources.
	let engineBGoneTimer: ReturnType<typeof setTimeout> | undefined;
	$effect(() => {
		if (!isDualEngine || !mounted) return;
		const bState = connectionManager.engineConnectionState('B');
		const status = syncHealth?.status;

		if (bState === 'disconnected' && (status === 'single-engine' || status === 'disconnected')) {
			if (!engineBGoneTimer) {
				engineBGoneTimer = setTimeout(() => {
					// Re-check conditions after timeout (state may have changed)
					const currentBState = connectionManager.engineConnectionState('B');
					if (currentBState === 'disconnected' && pipelineManager.warmStandbyActive) {
						pipelineManager.destroyEngineBPipeline();
						connectionManager.setWarmStandbyDegraded(false);
						notify('info', 'Engine B offline — warm standby disabled');
					}
				}, 30_000);
			}
		} else {
			if (engineBGoneTimer) {
				clearTimeout(engineBGoneTimer);
				engineBGoneTimer = undefined;
			}
		}

		return () => {
			if (engineBGoneTimer) {
				clearTimeout(engineBGoneTimer);
				engineBGoneTimer = undefined;
			}
		};
	});

	// Mid-session control plane re-discovery: when an engine becomes unreachable,
	// periodically poll the control plane for updated engine URLs. This catches
	// cases where an engine VM is replaced (new IP) while the session is live.
	const cpDiscovery = new ControlPlaneDiscovery(window.location.hostname);
	$effect(() => {
		if (!isDualEngine || !mounted) return;
		const status = syncHealth?.status;
		if (status === 'single-engine' || status === 'disconnected') {
			cpDiscovery.startPolling(10_000, (urls) => {
				// If a new engine B URL is discovered, update the engine list
				const currentEngines = getEngineUrls();
				const currentBUrl = currentEngines?.find(e => e.id === 'B')?.url;
				if (urls.engineBUrl && urls.engineBUrl !== currentBUrl) {
					// setEngineUrls() fires onNewEngineDiscovered synchronously, which
					// triggers forceSnapshotPush('A') → acquireLock('B'). dualApi must
					// know about B before that runs or the lock throws and the force is
					// silently dropped. Register with the managers first.
					connectionManager.addEngine('B', urls.engineBUrl);
					dualApi.addEngine('B', urls.engineBUrl);
					// Rebuild the warm-standby MediaPipeline immediately against the
					// new URL. The enableWarmStandby $effect alone would lag until
					// `store.state.engineBUrl` (authoritative state) catches up, which
					// races the 30s engine-gone timer. Driving the rebuild from the
					// discovery callback closes that gap.
					pipelineManager.enableWarmStandby(urls.engineBUrl, Object.keys(store.state.sources ?? {}), {
						onControlState: (data) => connectionManager.handleControlData('B', data),
					});
					setEngineUrls([
						{ id: 'A', url: urls.engineAUrl },
						{ id: 'B', url: urls.engineBUrl },
					]);
					notify('info', 'Engine B re-discovered via control plane');
				}
			});
		} else {
			cpDiscovery.stopPolling();
		}
		return () => cpDiscovery.stopPolling();
	});

	/** After automatic failover, register a one-time user gesture listener
	 *  to resume audio (browser autoplay policy blocks AudioContext.resume()
	 *  without a user gesture). */
	function requestAudioResumeAfterFailover() {
		const handler = () => {
			pipelineManager.resumeActiveAudio();
			pflManager.resumeContext();
			document.removeEventListener('click', handler);
			document.removeEventListener('keydown', handler);
		};
		document.addEventListener('click', handler, { once: true });
		document.addEventListener('keydown', handler, { once: true });
	}

	// Pre-warm audio contexts on first user interaction so automatic
	// failover can resume audio without requiring another gesture.
	// AudioContext.resume() needs a user gesture — do it once, early.
	function preWarmAudioOnGesture() {
		if (audioPreWarmed) return;
		audioPreWarmed = true;
		pipelineManager.preWarmAudioContexts();
		document.removeEventListener('click', preWarmAudioOnGesture);
		document.removeEventListener('keydown', preWarmAudioOnGesture);
	}
	document.addEventListener('click', preWarmAudioOnGesture, { once: false });
	document.addEventListener('keydown', preWarmAudioOnGesture, { once: false });

	$effect(() => {
		if (!isDualEngine || !mounted) return;
		const status = syncHealth?.status;
		const auth = syncHealth?.authoritative;

		// Failover: single-engine with B authoritative, currently showing A
		if (status === 'single-engine' && auth === 'B' && pipelineManager.activeEngine === 'A' && !videoFailoverActive) {
			const engineBUrl = store.state.engineBUrl;
			if (engineBUrl) {
				if (!canFailover()) {
					console.warn('[failover] Circuit breaker: max failovers reached in window');
					notify('warning', 'Video failover suppressed — both engines may be degraded');
					return;
				}
				recordFailover();
				videoFailoverActive = true;
				stallFailover = false;
				pipelineManager.failoverToEngineB(engineBUrl, Object.keys(store.state.sources ?? {}))
					.then(() => {
						activeEngine = 'B';
						notify('warning', 'Video switched to Engine B — click to resume audio');
						requestAudioResumeAfterFailover();
					})
					.catch(err => {
						console.warn('Video failover failed:', err);
						notify('error', 'Video failover to Engine B failed');
					});
			}
		}

		// Restore: when synced and currently on B, switch back to A.
		// Restoring to Engine A is a safe operation — do NOT consume
		// the failover circuit breaker budget.
		if (status === 'synced' && pipelineManager.activeEngine === 'B' && videoFailoverActive && !stallFailover) {
			videoFailoverActive = false;
			pipelineManager.restoreToEngineA(Object.keys(store.state.sources ?? {}))
				.then(() => {
					activeEngine = 'A';
					notify('info', 'Video restored to Engine A — click to resume audio');
					requestAudioResumeAfterFailover();
				})
				.catch(err => {
					console.warn('Video restore failed:', err);
					notify('error', 'Video restore to Engine A failed');
				});
		}
	});

	// Auto-recover from manual A/B toggle when active engine disconnects.
	// The reconciler-driven failover above only fires when syncHealth reports
	// 'single-engine'. But if the operator manually toggled to Engine B and B
	// dies, the reconciler may still think A is authoritative — leaving the
	// operator stuck watching a dead B feed. This effect reacts directly to
	// connection state for immediate recovery.
	$effect(() => {
		if (!isDualEngine || !mounted) return;
		// Skip if the reconciler-driven failover is already handling things
		if (videoFailoverActive) return;

		// Read transportChangeCount so Svelte re-runs when engine state changes
		void transportChangeCount;

		const active = pipelineManager.activeEngine;
		const activeConnected = connectionManager.engineConnectionState(active) !== 'disconnected';
		const other = active === 'A' ? 'B' : 'A';
		const otherConnected = connectionManager.engineConnectionState(other) !== 'disconnected';

		if (!activeConnected && otherConnected) {
			if (active === 'B') {
				// Restoring to Engine A is safe — do NOT consume circuit breaker budget
				pipelineManager.restoreToEngineA(Object.keys(store.state.sources ?? {}))
					.then(() => {
						activeEngine = 'A';
						stallFailover = false;
						notify('info', 'Switched back to Engine A — click to resume audio');
						requestAudioResumeAfterFailover();
					})
					.catch(err => {
						console.warn('A/B auto-recover to A failed:', err);
						notify('error', 'Auto-recover to Engine A failed');
					});
			} else if (active === 'A' && store.state.engineBUrl) {
				if (!canFailover()) {
					console.warn('[failover] Circuit breaker: max failovers reached in window');
					notify('warning', 'Video failover suppressed — both engines may be degraded');
					return;
				}
				recordFailover();
				videoFailoverActive = true;
				stallFailover = false;
				pipelineManager.failoverToEngineB(store.state.engineBUrl, Object.keys(store.state.sources ?? {}))
					.then(() => {
						activeEngine = 'B';
						notify('warning', 'Switched to Engine B — click to resume audio');
						requestAudioResumeAfterFailover();
					})
					.catch(err => {
						console.warn('A/B auto-recover to B failed:', err);
						notify('error', 'Auto-recover to Engine B failed');
					});
			}
		}
	});

	// Create/destroy FastControl for engine A based on WebTransport connection state.
	// Both engines receive datagrams via DualFastControl when both transports are available.
	$effect(() => {
		if (connectionState === 'webtransport' && !fastControlA) {
			const wt = pipeline.getSourceTransport('program-preview');
			if (wt) {
				try {
					fastControlA = createFastControl(wt, {
						onWriteError: handleFastControlWriteError,
						ptpNow: isDualEngine ? () => connectionManager.clock.ptpNow() : undefined,
						engineId: 'A',
						commitTransitionPosition: isDualEngine ? (pos) => { setTransitionPosition(pos).catch(() => {}); } : undefined,
						commitDVESlotTransform: isDualEngine ? (slotId, posX, posY, width, height, rotation, opacity) => {
							updateDVESlot(slotId, { positionX: posX, positionY: posY, width, height, rotationDeg: rotation, opacity }).catch(() => {});
						} : undefined,
					});
					// Route clock sync pings through FastControl's shared writer
					connectionManager.setDatagramSender('A', (data) => fastControlA?.writeDatagram(data));
				} catch {
					// Datagrams not supported — fall back to REST
				}
			}
		}
		if (connectionState !== 'webtransport' && fastControlA) {
			connectionManager.setDatagramSender('A', null);
			fastControlA.close();
			fastControlA = null;
		}
	});

	// Create/destroy FastControl for engine B when its transport becomes available.
	$effect(() => {
		if (!isDualEngine) return;
		// Read transportChangeCount to create reactive dependency — connectionManager.getTransport()
		// is not $state, so the effect won't re-run when Engine B's WebTransport connects.
		const _trigger = transportChangeCount;
		const transportB = connectionManager.getTransport('B');
		if (transportB && !fastControlB) {
			try {
				fastControlB = createFastControl(transportB, {
					onWriteError: handleFastControlWriteError,
					ptpNow: () => connectionManager.clock.ptpNow(),
					engineId: 'B',
					commitTransitionPosition: (pos) => { setTransitionPosition(pos).catch(() => {}); },
					commitDVESlotTransform: (slotId, posX, posY, width, height, rotation, opacity) => {
						updateDVESlot(slotId, { positionX: posX, positionY: posY, width, height, rotationDeg: rotation, opacity }).catch(() => {});
					},
				});
				connectionManager.setDatagramSender('B', (data) => fastControlB?.writeDatagram(data));
			} catch {
				// Datagrams not supported on engine B
			}
		}
		if (!transportB && fastControlB) {
			connectionManager.setDatagramSender('B', null);
			fastControlB.close();
			fastControlB = null;
		}
	});

	// Compare mode: create Engine B pipeline when entering, detach compare canvas on exit.
	// If warm standby is active, preserve the pipeline (only detach the compare canvas).
	// If warm standby is not active, destroy the pipeline entirely.
	// store.state is replaced on every state update; depending on the URL
	// string alone keeps the cleanup below from detaching the compare canvas
	// each time.
	const compareEngineBUrl = $derived(store.state.engineBUrl);
	$effect(() => {
		// The cleanup belongs to a compare that was set up. Returned from a
		// run outside compare mode, it ran as compare mode began and detached
		// the canvas ProgramCompare had just attached.
		if (!compareMode || !compareEngineBUrl) return;
		pipelineManager.createEngineBPipeline(compareEngineBUrl);
		pipelineManager.connectSourceB('program-preview');
		return () => {
			if (pipelineManager.hasEngineBPipeline) {
				pipelineManager.detachCanvasB('program-preview', 'compare-b');
				if (!pipelineManager.warmStandbyActive) {
					// No warm standby — safe to destroy entirely.
					// The A/B toggle effect will recreate if activeEngine === 'B'.
					pipelineManager.destroyEngineBPipeline();
				}
			}
		};
	});

	// A/B toggle: switch program canvas between Engine A and Engine B pipelines.
	// The PipelineManager tracks the current program canvas element internally.
	$effect(() => {
		if (!isDualEngine || !mounted) return;
		if (activeEngine === 'B' && store.state.engineBUrl) {
			// Re-run on every state update; switching again detaches and
			// disconnects Engine A's sources each time. A destroyed pipeline
			// leaves activeEngine at 'B', and is recreated below.
			if (pipelineManager.activeEngine === 'B' && pipelineManager.hasEngineBPipeline) return;
			if (!pipelineManager.hasEngineBPipeline) {
				pipelineManager.createEngineBPipeline(store.state.engineBUrl);
				pipelineManager.connectSourceB('program-preview');
			}
			pipelineManager.switchProgramToEngineB();
		} else if (activeEngine === 'A' && pipelineManager.activeEngine === 'B') {
			pipelineManager.switchProgramToEngineA();
			stallFailover = false;
		}
	});

	async function submitToken() {
		if (!tokenInput.trim()) return;
		setAuthToken(tokenInput.trim());
		tokenRequired = false;
		connectionError = null;
		initialLoading = true;
		await connectionManager.start();
	}

	onMount(async () => {
		keyboard.attach();
		document.addEventListener('keydown', handleGlobalShortcuts);
		mounted = true;
		syncInterval = setInterval(() => { now = Date.now(); }, 1000);
		// Reconciler tick interval is started lazily when engine B is detected
		// (see onStateUpdate callback).

		// Bootstrap: discover QUIC server address for WebTransport.
		// API calls always use relative URLs (same-origin in prod, Vite proxy in dev).
		// The QUIC server (:8080) is UDP-only — regular fetch() needs TCP, so we never
		// redirect API base URL to the QUIC origin.
		try {
			await fetchServerInfo();
		} catch {
			// Will retry via connection manager
		}

		// Connect the program-preview MoQ stream (source was added during
		// init so the canvas can attach before onMount via ProgramPreview's
		// $effect).
		pipeline.connectSource('program-preview');

		// Resume AudioContexts on first user gesture (browser autoplay policy).
		const resumeAudio = () => {
			pipeline.resumeAllAudio();
			pflManager.resumeContext();
			document.removeEventListener('click', resumeAudio);
			document.removeEventListener('keydown', resumeAudio);
		};
		document.addEventListener('click', resumeAudio, { once: true });
		document.addEventListener('keydown', resumeAudio, { once: true });

		// Start audio metering rAF loop
		pipelineManager.startMetering();

		// Start frame stall detection (alerts when a source stops sending frames)
		pipeline.startStallDetection();

		// Load macro list for keyboard shortcuts
		refreshMacros();

		// Attempt operator reconnection from stored token
		if (operatorState.hasStoredToken()) {
			await operatorState.reconnect();
		}

		// Push immediately on discovery so the new engine catches up on
		// commands missed during the pre-discovery window, rather than waiting
		// for the reconciler's next 2s divergence tick.
		setOnNewEngineDiscovered((_engineId) => {
			autoReconcile.forceSnapshotPush(syncHealth?.authoritative ?? 'A');
		});

		// Fetch initial state, start polling, and attempt WebTransport connection
		await connectionManager.start();
	});

	onDestroy(() => {
		keyboard.detach();
		document.removeEventListener('keydown', handleGlobalShortcuts);
		document.removeEventListener('click', preWarmAudioOnGesture);
		document.removeEventListener('keydown', preWarmAudioOnGesture);
		clearTimeout(announcementTimer);
		clearInterval(syncInterval);
		clearInterval(reconcilerInterval);
		if (audioDriftInterval) {
			clearInterval(audioDriftInterval);
			audioDriftInterval = undefined;
		}
		setOnNewEngineDiscovered(null);
		cpDiscovery.stopPolling();
		autoReconcile.destroy();
		fastControlA?.close();
		fastControlB?.close();
		captionRenderer?.destroy();
		captionRenderer = null;
		pflManager.destroy();
		pipelineManager.destroy();
		connectionManager.stop();
		pipeline.destroy();
		operatorState.destroy();
		setEngineUrls(null);
		setDualTimestampProvider(null);
		setPartialFailureCallback(null);
		setPartialFailureNotify(null);
		mounted = false;
	});
</script>

<LoadingOverlay loading={initialLoading} error={connectionError} />

{#if tokenRequired}
	<div class="token-prompt">
		<div class="token-box">
			<p>API token required</p>
			<form onsubmit={(e) => { e.preventDefault(); submitToken(); }}>
				<input
					type="password"
					bind:value={tokenInput}
					placeholder="Paste API token"
					autocomplete="off"
				/>
				<button type="submit">Connect</button>
			</form>
		</div>
	</div>
{/if}

{#if showOperatorRegistration}
	<OperatorRegistration onRegistered={() => { showOperatorRegistration = false; }} />
{/if}

<ProgramHealthBanner
	programSource={store.state.programSource}
	status={store.state.sources[store.state.programSource]?.status ?? 'healthy'}
/>

<ErrorBoundary>
	<ConnectionBanner {connectionState} {syncStatus} engineStatus={engineBannerStatus} {perEngineState} {sessionFallbackUrl} {engineFallbackUrl} servingEngine={servingEngine} />
	<Toast />
	{#if layoutMode === 'simple'}
		<SimpleMode
			crState={store.effectiveState}
			onSwitchLayout={switchLayout}
			{onCanvasReady}
			onPreview={(key) => {
				store.optimisticPreview(key);
				apiCall(setPreview(key), 'Preview failed');
			}}
			onCut={() => cutToPreview(store)}
			onDissolve={() => autoTransitionToPreview(store, 'mix', 1000, 'Dissolve failed')}
		/>
	{:else if layoutMode === 'on-air'}
		<OnAirController
			crState={store.effectiveState}
			{onCanvasReady}
			onSwitchLayout={switchLayout}
			{isDualEngine}
			engineAHealthy={engineHealth?.A?.connected ?? true}
			engineBHealthy={engineHealth?.B?.connected ?? true}
			{activeEngine}
			authoritativeEngine={syncHealth?.authoritative ?? 'A'}
			onEngineToggle={(e) => activeEngine = e}
			syncStatus={syncHealth?.status ?? 'disconnected'}
			onSyncClick={() => { redundancyPanelVisible = true; }}
			onCut={(source) => {
				if (source) {
					store.optimisticCut(source);
					apiCall(cut(source), 'Cut failed');
				}
			}}
			onAuto={(source) => {
				if (source) {
					apiCall(startTransition(source, transitionType, transitionDuration), 'Transition failed');
				}
			}}
			{transitionType}
			{transitionDuration}
			onTransitionTypeChange={(type) => { transitionType = type; }}
			onTransitionDurationChange={(ms) => { transitionDuration = ms; }}
			onBugToggle={() => {
				const gfxLayers = store.state.graphics?.layers ?? [];
				if (gfxLayers.some((l) => l.active)) {
					const offs = gfxLayers.filter((l) => l.active).map((l) => graphicsAutoOff(l.id));
					apiCall(Promise.all(offs).then((r) => r[0]), 'Bug toggle failed');
				} else if (gfxLayers.length > 0) {
					apiCall(graphicsAutoOn(gfxLayers[0].id), 'Bug toggle failed');
				}
			}}
			onScteInsert={() => {
				apiCall(scte35Cue({ commandType: 'splice_insert', isOut: true, durationMs: 60000, autoReturn: true }), 'SCTE insert failed');
			}}
			onScteReturn={() => {
				apiCall(scte35Return(), 'SCTE return failed');
			}}
			onFirePod={() => {
				const channel = store.effectiveState.playout?.channels?.[0];
				const pods = channel?.pods ?? [];
				const armedPod = pods.find((p) => p.armed);
				if (armedPod) {
					apiCall(podFire(1, armedPod.id), 'Fire pod failed');
				}
			}}
			onReturnToLive={() => {
				apiCall(scte35Return(), 'Return to live failed');
			}}
			onHold={() => {
				const ch = store.effectiveState.playout?.channels?.find(c => c.id === 1) ?? store.effectiveState.playout?.channels?.[0];
					if (ch?.mode === 'manual') {
						apiCall(playoutReturnToAuto(1), 'Return to auto failed');
					} else {
						apiCall(playoutTakeManual(1), 'Hold failed');
					}
			}}
			onSkip={() => {
				apiCall(playoutNext(1), 'Skip failed');
			}}
			onTakeNext={() => {
				apiCall(playoutNext(1), 'Take next failed');
			}}
			onRecordToggle={() => {
				if (store.effectiveState.recording?.active) {
					apiCall(stopRecording(), 'Stop recording failed');
				} else {
					apiCall(startRecording(), 'Start recording failed');
				}
			}}
			onStateUpdate={store.applyUpdate}
			{pipeline}
			{fastControl}
			getTransport={() => pipeline.getSourceTransport('program-preview')}
		/>
	{:else}
		<div class="control-room">
			<header class="header">
				<div class="header-row">
					<OutputControls state={store.effectiveState} {connectionState} {switchLayout}
					onToggleIOPanel={() => { ioPanelVisible = !ioPanelVisible; }}
					{ioPanelVisible}
					onToggleComms={() => { commsVisible = !commsVisible; }}
					commsActive={store.effectiveState.comms?.active ?? false}
					{syncHealth}
					{isDualEngine}
					{engineBState}
					activeEngineId={activeEngine}
					onToggleRedundancyPanel={() => { redundancyPanelVisible = !redundancyPanelVisible; }}
				/>
					<div class="header-right">
						<OperatorBadge state={store.effectiveState} />
						{#if !operatorState.isRegistered() && (store.effectiveState.operators?.length ?? 0) > 0}
							<button class="register-btn" onclick={() => { showOperatorRegistration = true; }}>Register</button>
						{:else if !operatorState.isRegistered()}
							<button class="register-btn" onclick={() => { showOperatorRegistration = true; }}>Register Operator</button>
						{/if}
					</div>
				</div>
			</header>

			<CommsBar
				commsState={store.effectiveState.comms}
				operatorId={operatorState.getSession()?.id ?? ''}
				operatorName={operatorState.getSession()?.name ?? ''}
				visible={commsVisible}
				onToggle={() => commsVisible = !commsVisible}
				getTransport={() => pipeline.getSourceTransport('program-preview')}
				getTransportForEngine={isDualEngine ? (engineId) => connectionManager.getTransport(engineId) : undefined}
				authoritativeEngine={syncHealth?.authoritative}
				onCommsActive={(active) => pflManager.setCommsActive(active)}
				onDimToggle={() => { commsDimmed = !commsDimmed; pflManager.setDim(commsDimmed); }}
				dimmed={commsDimmed}
			/>

			<section class="monitors">
				{#if compareMode}
					<ProgramCompare
						engineAMeta={{ rtt: engineHealth?.A?.rtt ?? 0, seq: engineHealth?.A?.lastSeq ?? 0, healthy: engineHealth?.A?.connected ?? false }}
						engineBMeta={{ rtt: engineHealth?.B?.rtt ?? 0, seq: engineHealth?.B?.lastSeq ?? 0, healthy: engineHealth?.B?.connected ?? false }}
						formatWidth={store.state.pipelineFormat?.width}
						formatHeight={store.state.pipelineFormat?.height}
						onExit={() => compareMode = false}
						onCanvasAReady={(canvas) => {
							pipelineManager.syncProgramPreviewCanvases(store.effectiveState.previewSource, canvas);
						}}
						onCanvasBReady={(canvas) => {
							pipelineManager.attachCanvasB('program-preview', 'compare-b', canvas);
						}}
					/>
				{:else}
					<ProgramPreview state={store.effectiveState} {onCanvasReady} {onCaptionElReady} showLayoutOverlay={layoutTabActive} showGraphicsOverlay={graphicsTabActive} {fastControl} onGraphicsLayerSelect={(id) => { graphicsSelectedLayerId = id; }}
						{isDualEngine}
						{activeEngine}
						onEngineToggle={(e) => activeEngine = e}
						engineAConnected={engineHealth?.A?.connected ?? true}
						engineBConnected={engineHealth?.B?.connected ?? true}
					/>
				{/if}
			</section>

			<section class="control-strip">
				<div class="source-buttons">
					{#each sortedSourceKeys(store.effectiveState.sources) as key, i}
						<SourceButton
							source={store.effectiveState.sources[key]}
							tally={store.effectiveState.tallyState[key] || 'idle'}
							index={i}
							audioLevelDb={store.effectiveState.audioChannels?.[key] ? Math.max(store.effectiveState.audioChannels[key].peakL, store.effectiveState.audioChannels[key].peakR) : undefined}
							layoutSlots={store.effectiveState.layout?.slots}
							onPreview={() => { store.optimisticPreview(key); apiCall(setPreview(key), 'Preview failed'); }}
							onCut={() => { store.optimisticCut(key); apiCall(cut(key), 'Cut failed'); }}
							onLabelChange={handleLabelChange}
						/>
					{/each}
				</div>
				<ContextualActions state={store.effectiveState} />
				<TransitionControls state={store.effectiveState} pendingConfirm={keyboard.pendingConfirmAction} {fastControl} />
			</section>

			<section class="bottom-panel">
					<BottomTabs
					replayActive={store.effectiveState.replay?.state === 'playing' || store.effectiveState.replay?.state === 'paused'}
	onTabChange={(tab) => { activeBottomTab = tab; }}
				>
					{#snippet children(activeTab)}
						{#if activeTab === 'Audio'}
							<div class="tab-panel audio-tab">
								<AudioMixer state={store.effectiveState} {sourceLevels} {programLevels} {pflActiveSource} pflMode={pflActiveMode} {pflManager} selectedChannel={selectedAudioChannel} {audioDrift} {isDualEngine} engineBAudioChannels={engineBState?.audioChannels ?? null} onPFLToggle={handlePFLToggle} onStateUpdate={store.applyUpdate} onChannelSelect={(key) => { selectedAudioChannel = key; }} />
							</div>
						{:else if activeTab === 'Compose'}
							<div class="tab-panel">
								<CompositionPanel state={store.effectiveState} bind:activeSubTab={composeSubTab} externalSelectedLayerId={graphicsSelectedLayerId} {fastControl} />
							</div>
						{:else if activeTab === 'Macros'}
							<div class="tab-panel">
								<MacroPanel state={store.effectiveState} />
							</div>
						{:else if activeTab === 'Replay'}
							<div class="tab-panel">
								<ReplayPanel state={store.effectiveState} {pipeline} />
							</div>
						{:else if activeTab === 'SCTE'}
							<div class="tab-panel">
								{#if store.effectiveState.scte35?.enabled}
									<SCTE35Panel state={store.effectiveState} onStateUpdate={store.applyUpdate} />
								{:else}
									<div class="panel-disabled">SCTE-35 not enabled on server</div>
								{/if}
							</div>
						{:else if activeTab === 'Captions'}
							<div class="tab-panel">
								<CaptionsPanel state={store.effectiveState} />
							</div>
						{:else if activeTab === 'Clips'}
							<div class="tab-panel">
								<ClipsPanel state={store.effectiveState} />
							</div>
						{:else if activeTab === 'Team'}
							<div class="tab-panel tab-panel--team">
								<TeamPanel state={store.effectiveState} />
							</div>
						{:else if activeTab === 'Video'}
							<div class="tab-panel">
								<VideoProcessingPanel state={store.effectiveState} />
							</div>
						{/if}
						<!-- PlayoutPanel always mounted to preserve state across tab switches -->
						<div class="tab-panel" style:display={activeTab === 'Playout' ? '' : 'none'}>
							<PlayoutPanel state={store.effectiveState} {isDualEngine} getCacheStatus={playoutEngineCacheStatus} />
						</div>
					{/snippet}
				</BottomTabs>
			</section>
		</div>

		{#if showOverlay}
			<KeyboardOverlay onclose={() => showOverlay = false} />
		{/if}
	{/if}
</ErrorBoundary>

<StatsPanel visible={statsPanelVisible} onclose={() => { statsPanelVisible = false; }} onopengraph={() => { pipelineGraphVisible = true; statsPanelVisible = false; }} {pipeline} pipelineCodec={store.effectiveState.pipelineFormat?.codec} pipelineColorFormat={store.effectiveState.pipelineFormat?.colorFormat} />
<PipelineGraph visible={pipelineGraphVisible} onclose={() => { pipelineGraphVisible = false; }} {pipeline} />
<IOPanel visible={ioPanelVisible} state={store.effectiveState} onclose={() => { ioPanelVisible = false; }} {isDualEngine} engineSourceHealth={ioSourceHealth} engineEndpoints={ioEngineEndpoints} engineDestinations={ioEngineDestinations} />
{#if isDualEngine}
	<RedundancyPanel
		health={syncHealth ?? { status: 'disconnected', authoritative: 'A', divergences: [], engineA: { lastSeen: 0, lastCommandSeq: 0, connected: false }, engineB: { lastSeen: 0, lastCommandSeq: 0, connected: false }, warnings: [], staleEngines: [] }}
		engineHealth={engineHealth ?? { A: { connected: false, rtt: 0, clockOffset: 0, lastSeq: 0, uptime: 0, lastSeen: 0, sources: {} }, B: { connected: false, rtt: 0, clockOffset: 0, lastSeq: 0, uptime: 0, lastSeen: 0, sources: {} } }}
		divergenceLog={divergenceLog.entries}
		visible={redundancyPanelVisible}
		onclose={() => redundancyPanelVisible = false}
		onforcereconcile={handleForceReconcile}
		oncompare={handleCompare}
		playoutComparison={playoutComparison}
		{audioComparison}
		{outputStatus}
		engineBUrl={store.state.engineBUrl}
	/>
{/if}
<div class="sr-only" aria-live="polite" role="status">{announcement}</div>

<style>
	@import '$lib/layout/responsive.css';

	.control-room {
		display: grid;
		grid-template-rows: auto 1fr auto auto;
		height: 100vh;
		background: var(--bg-base);
	}

	.header {
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-default);
	}

	.monitors {
		background: var(--bg-base);
		min-height: 0;
		overflow: hidden;
	}

	.control-strip {
		display: flex;
		align-items: stretch;
		gap: 4px;
		padding: 4px 8px;
		border-top: 1px solid var(--border-default);
		background: var(--bg-surface);
		height: 82px;
		flex-shrink: 0;
	}

	.source-buttons {
		display: flex;
		gap: 3px;
		overflow-x: auto;
		overflow-y: hidden;
		flex-shrink: 0;
		align-items: stretch;
	}

	.source-buttons::-webkit-scrollbar {
		height: 2px;
	}

	.source-buttons::-webkit-scrollbar-thumb {
		background: var(--border-subtle);
		border-radius: 1px;
	}

	.bottom-panel {
		border-top: 1px solid var(--border-default);
		background: var(--bg-surface);
		height: clamp(300px, 45vh, 640px);
		overflow: hidden;
	}

	.tab-panel {
		height: 100%;
		overflow: hidden;
	}

	.tab-panel.audio-tab {
		overflow-x: auto;
		overflow-y: hidden;
	}

	.tab-panel--team {
		display: flex;
		flex-direction: column;
		overflow: hidden;
		min-height: 0;
	}

	.header-row {
		display: flex;
		align-items: center;
	}

	.header-right {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-left: auto;
		padding: 0 8px;
	}

	.register-btn {
		padding: 4px 10px;
		border: 1px solid var(--border-subtle, #444);
		border-radius: 4px;
		background: transparent;
		color: var(--text-secondary, #aaa);
		font-size: var(--text-sm);
		cursor: pointer;
	}

	.register-btn:hover {
		border-color: var(--text-secondary, #888);
		color: var(--text-primary, #eee);
	}


	.token-prompt {
		position: fixed;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--overlay-opaque);
		z-index: var(--z-system);
	}

	.token-box {
		background: var(--bg-surface, #1e1e1e);
		border: 1px solid var(--border-subtle, #444);
		border-radius: 8px;
		padding: 24px;
		text-align: center;
	}

	.token-box p {
		margin: 0 0 12px;
		color: var(--text-primary, #eee);
		font-size: var(--text-base);
	}

	.token-box form {
		display: flex;
		gap: 8px;
	}

	.token-box input {
		padding: 6px 10px;
		border: 1px solid var(--border-subtle, #555);
		border-radius: 4px;
		background: var(--bg-base, #111);
		color: var(--text-primary, #eee);
		font-family: monospace;
		font-size: var(--text-sm);
		width: 320px;
	}

	.token-box button {
		padding: 6px 16px;
		border: none;
		border-radius: 4px;
		background: #2563eb;
		color: #fff;
		font-size: var(--text-sm);
		cursor: pointer;
	}

	.token-box button:hover {
		background: #1d4ed8;
	}

	.panel-disabled {
		display: flex;
		align-items: center;
		justify-content: center;
		height: 100%;
		color: var(--text-tertiary);
		font-family: var(--font-ui);
		font-size: var(--text-md);
	}
</style>

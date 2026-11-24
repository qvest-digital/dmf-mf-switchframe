/**
 * Which engine of a redundant pair takes a switching command.
 *
 * An engine passes a cut, preview, transition, T-bar position or
 * squeezeback command it takes on to its peer, so the page sends each to one
 * engine: the leader while it is on air, else the other.
 */

/** GET /api/peer/health, as far as the choice reads it. */
export interface PeerHealth {
	status?: string;
	engineLabel?: string;
	programSource?: string;
	epoch?: number;
	/** Set by an engine that passes a switching command on to its peer. */
	relays?: boolean;
}

/** The engine ids in the order to try them: reachable before unreachable,
 * on air before starting, then the leader as switchframe decides it, the
 * higher epoch and else the lower label. */
export function orderByHealth(entries: { id: string; health: PeerHealth | null }[]): string[] {
	const rank = (h: PeerHealth | null, id: string): [number, number, number, string] => [
		h?.status === 'ok' ? 0 : 1,
		h?.programSource ? 0 : 1,
		-(h?.epoch ?? 0),
		h?.engineLabel || id,
	];
	return [...entries]
		.map((e) => ({ id: e.id, r: rank(e.health, e.id) }))
		.sort((a, b) => {
			for (let i = 0; i < 3; i++) {
				if (a.r[i] !== b.r[i]) return (a.r[i] as number) - (b.r[i] as number);
			}
			return a.r[3] < b.r[3] ? -1 : a.r[3] > b.r[3] ? 1 : 0;
		})
		.map((e) => e.id);
}

/** Reads each engine's peer health every intervalMs and keeps the order to
 * send switching commands in. */
export class RelayTargetPoller {
	private current: string[] = [];
	private healths = new Map<string, PeerHealth | null>();
	private timer: ReturnType<typeof setInterval> | null = null;

	constructor(
		private engines: () => { id: string; url: string }[],
		private fetchFn: typeof fetch = (...a) => fetch(...a),
		private intervalMs = 1000,
		private timeoutMs = 800,
	) {}

	/** The engine ids in the order to send to; until the first read, the
	 * engines as configured. */
	order(): string[] {
		const ids = this.engines().map((e) => e.id);
		const known = this.current.filter((id) => ids.includes(id));
		return [...known, ...ids.filter((id) => !known.includes(id))];
	}

	/** The engine ids in the order to send a switching command to, or null
	 * while the first does not pass it on, as before its first read or on
	 * an engine image that predates relaying: then each command goes to
	 * both engines. */
	singleSendOrder(): string[] | null {
		const order = this.order();
		return this.healths.get(order[0])?.relays === true ? order : null;
	}

	start(): void {
		if (this.timer) return;
		void this.poll();
		this.timer = setInterval(() => void this.poll(), this.intervalMs);
	}

	stop(): void {
		if (this.timer) clearInterval(this.timer);
		this.timer = null;
	}

	async poll(): Promise<void> {
		const entries = await Promise.all(this.engines().map(async (e) => {
			try {
				const ctrl = new AbortController();
				const t = setTimeout(() => ctrl.abort(), this.timeoutMs);
				try {
					const res = await this.fetchFn(`${e.url}/api/peer/health`, { signal: ctrl.signal });
					return { id: e.id, health: res.ok ? ((await res.json()) as PeerHealth) : null };
				} finally {
					clearTimeout(t);
				}
			} catch {
				return { id: e.id, health: null };
			}
		}));
		this.current = orderByHealth(entries);
		this.healths = new Map(entries.map((e) => [e.id, e.health]));
	}
}

/** Starts poller and has switching commands follow it (see
 * setRelayTargetProvider); the returned func stops both. */
export function wireRelayTarget(
	poller: RelayTargetPoller,
	setProvider: (provider: (() => string[] | null) | null) => void,
): () => void {
	setProvider(() => poller.singleSendOrder());
	poller.start();
	return () => {
		poller.stop();
		setProvider(null);
	};
}

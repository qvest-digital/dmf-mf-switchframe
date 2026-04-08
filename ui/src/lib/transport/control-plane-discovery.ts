/**
 * Discovers engine URLs by polling the control plane session health endpoint.
 * Tertiary discovery mechanism for when:
 * 1. Serving engine dies before delivering engineBUrl in state
 * 2. DNS inference hasn't found engineB yet
 */
export class ControlPlaneDiscovery {
	readonly sessionId: string | null;
	readonly controlPlaneUrl: string | null;

	constructor(hostname: string) {
		const parts = hostname.split('.');
		if (parts.length < 3 || parts.every((p) => /^\d+$/.test(p))) {
			this.sessionId = null;
			this.controlPlaneUrl = null;
			return;
		}
		this.sessionId = parts[0].replace(/-b$/, '');
		const domainParts = parts.slice(1);
		if (domainParts[0] === 'engines') {
			this.controlPlaneUrl = `https://control-plane.${domainParts.slice(1).join('.')}`;
		} else {
			this.controlPlaneUrl = null;
		}
	}

	async fetchEngineUrls(
		timeoutMs: number = 3000
	): Promise<{ engineAUrl: string; engineBUrl: string } | null> {
		if (!this.sessionId || !this.controlPlaneUrl) return null;

		const controller = new AbortController();
		const timeout = setTimeout(() => controller.abort(), timeoutMs);

		try {
			const res = await fetch(
				`${this.controlPlaneUrl}/api/sessions/${this.sessionId}/health`,
				{ signal: controller.signal }
			);
			if (!res.ok) return null;
			const data = await res.json();
			const engines = data.engines as Array<{ label: string; status: string }>;
			const engineB = engines?.find((e) => e.label === 'b' && e.status === 'ready');
			if (!engineB) return null;

			const domain = this.controlPlaneUrl.replace('https://control-plane.', '');
			return {
				engineAUrl: `https://${this.sessionId}.engines.${domain}`,
				engineBUrl: `https://${this.sessionId}-b.engines.${domain}`
			};
		} catch {
			return null;
		} finally {
			clearTimeout(timeout);
		}
	}
}

import type { SRTSourceInfo } from '$lib/api/types';

export type SRTHealthLevel = 'green' | 'yellow' | 'red' | 'gray';

export function computeSRTHealth(srt: SRTSourceInfo | undefined): SRTHealthLevel | undefined {
	if (!srt) return undefined;
	if (!srt.connected) return 'gray';
	if (srt.lossRate > 1.0 || srt.rttMs > 200) return 'red';
	if (srt.lossRate > 0.1 || srt.rttMs > 100 || srt.recvBufMs < 20) return 'yellow';
	return 'green';
}

export function computeOutputHealth(
	destinations:
		| Array<{
				state: string;
				droppedPackets?: number;
				overflowCount?: number;
				error?: string;
			}>
		| undefined
): SRTHealthLevel | undefined {
	if (!destinations || destinations.length === 0) return undefined;
	const hasError = destinations.some(
		(d) => d.state === 'error' || (d.overflowCount ?? 0) > 0
	);
	if (hasError) return 'red';
	const active = destinations.filter((d) => d.state === 'active' || d.state === 'starting');
	if (active.length === 0) return undefined;
	const hasDrops = destinations.some((d) => (d.droppedPackets ?? 0) > 0);
	if (hasDrops) return 'yellow';
	return 'green';
}

export function formatUptime(ms: number): string {
	const totalSecs = Math.floor(ms / 1000);
	const hours = Math.floor(totalSecs / 3600);
	const mins = Math.floor((totalSecs % 3600) / 60);
	const secs = totalSecs % 60;
	if (hours > 0) return `${hours}h ${mins}m`;
	if (mins > 0) return `${mins}m ${secs}s`;
	return `${secs}s`;
}

export function formatPacketRate(count: number, rate: number): string {
	const formatted = count.toLocaleString();
	if (rate > 0) return `${formatted} (+${rate.toFixed(1)}/s)`;
	return formatted;
}

export function formatBitrate(kbps: number): string {
	if (kbps >= 1000) return `${(kbps / 1000).toFixed(1)} Mbps`;
	return `${kbps.toFixed(0)} kbps`;
}

export function formatBytes(bytes: number): string {
	if (bytes >= 1_000_000_000) return `${(bytes / 1_000_000_000).toFixed(1)} GB`;
	if (bytes >= 1_000_000) return `${(bytes / 1_000_000).toFixed(1)} MB`;
	if (bytes >= 1_000) return `${(bytes / 1_000).toFixed(1)} KB`;
	return `${bytes} B`;
}

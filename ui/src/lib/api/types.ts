export type TallyStatus = 'program' | 'preview' | 'idle';
export type SourceHealthStatus = 'healthy' | 'stale' | 'no_signal' | 'offline';

export interface SourceInfo {
	key: string;
	label?: string;
	status: SourceHealthStatus;
	lastFrameTime: number;
	width?: number;
	height?: number;
	codec?: string;
}

export interface AudioChannel {
	level: number;  // dB (-inf to +12)
	muted: boolean;
	afv: boolean;   // audio-follows-video
}

export interface ControlRoomState {
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurationMs: number;
	transitionPosition: number;
	inTransition: boolean;
	audioLevels: Record<string, number> | null;
	audioChannels: Record<string, AudioChannel> | null;
	masterLevel: number;
	programPeak: [number, number];
	tallyState: Record<string, TallyStatus>;
	sources: Record<string, SourceInfo>;
	seq: number;
	timestamp: number;
}

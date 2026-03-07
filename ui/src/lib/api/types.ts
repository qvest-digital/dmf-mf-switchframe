export type TallyStatus = 'program' | 'preview' | 'idle';
export type SourceHealthStatus = 'healthy' | 'stale' | 'no_signal' | 'offline';

export interface SourceInfo {
	key: string;
	label?: string;
	status: SourceHealthStatus;

	position?: number;
	delayMs?: number;
	isVirtual?: boolean;
}

export interface EQBand {
	frequency: number;
	gain: number;
	q: number;
	enabled: boolean;
}

export interface CompressorSettings {
	threshold: number;
	ratio: number;
	attack: number;
	release: number;
	makeupGain: number;
}

export interface AudioChannel {
	level: number;  // dB (-inf to +12)
	trim: number;   // dB (-20 to +20), input gain
	muted: boolean;
	afv: boolean;   // audio-follows-video
	peakL: number;  // dBFS
	peakR: number;  // dBFS
	eq: [EQBand, EQBand, EQBand];
	compressor: CompressorSettings;
	gainReduction: number;  // compressor GR in dB
	audioDelayMs?: number;  // lip-sync delay (0-500ms)
}

export interface RecordingStatus {
	active: boolean;
	filename?: string;
	bytesWritten?: number;
	durationSecs?: number;
	droppedPackets?: number;
	error?: string;
}

export interface SRTOutputStatus {
	active: boolean;
	mode?: 'caller' | 'listener';
	address?: string;
	port?: number;
	state?: string;
	connections?: number;
	bytesWritten?: number;
	droppedPackets?: number;
	overflowCount?: number;
	error?: string;
}

export interface SRTOutputConfig {
	mode: 'caller' | 'listener';
	address?: string;
	port: number;
	latency?: number;
	streamID?: string;
}

export interface DestinationConfig {
	type: 'srt-caller' | 'srt-listener';
	address?: string;
	port: number;
	latency?: number;
	streamID?: string;
	encryption?: string;
	passphrase?: string;
	maxBandwidth?: number;
	maxConns?: number;
	name?: string;
}

export interface DestinationInfo {
	id: string;
	name?: string;
	type: string;
	address?: string;
	port: number;
	state: string;
	bytesWritten?: number;
	droppedPackets?: number;
	connections?: number;
	error?: string;
}

export interface DestinationStatus {
	id: string;
	config: DestinationConfig;
	state: string;
	bytesWritten: number;
	droppedPackets: number;
	overflowCount?: number;
	connections?: number;
	error?: string;
	createdAt: string;
	startedAt?: string;
}

export interface GraphicsState {
	active: boolean;
	template?: string;
	fadePosition?: number;
}

export interface PresetInfo {
	id: string;
	name: string;
}

export interface Preset {
	id: string;
	name: string;
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurMs: number;
	audioChannels: Record<string, AudioChannelPreset>;
	masterLevel: number;
	createdAt: string;
}

export interface AudioChannelPreset {
	level: number;
	muted: boolean;
	afv: boolean;
}

export interface RecallPresetResponse {
	preset: Preset;
	warnings?: string[];
}

export interface MacroStep {
	action: 'cut' | 'preview' | 'transition' | 'wait' | 'set_audio';
	params: Record<string, unknown>;
}

export interface Macro {
	name: string;
	steps: MacroStep[];
}

export interface KeyConfig {
	type: 'chroma' | 'luma';
	enabled: boolean;
	keyColorY?: number;
	keyColorCb?: number;
	keyColorCr?: number;
	similarity?: number;
	smoothness?: number;
	spillSuppress?: number;
	lowClip?: number;
	highClip?: number;
	softness?: number;
	fillSource?: string;
}

export type ReplayPlayerState = 'idle' | 'loading' | 'playing';

export interface ReplayBufferInfo {
	source: string;
	frameCount: number;
	gopCount: number;
	durationSecs: number;
	bytesUsed: number;
}

export interface ReplayState {
	state: ReplayPlayerState;
	source?: string;
	speed?: number;
	loop?: boolean;
	position?: number;
	markIn?: number;     // Unix ms
	markOut?: number;    // Unix ms
	markSource?: string;
	buffers?: ReplayBufferInfo[];
}

export type OperatorRole = 'director' | 'audio' | 'graphics' | 'viewer';

export interface OperatorInfo {
	id: string;
	name: string;
	role: OperatorRole;
	connected: boolean;
}

export interface LockInfo {
	holderId: string;
	holderName: string;
	acquiredAt: number; // Unix ms
}

export interface ControlRoomState {
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurationMs: number;
	transitionPosition: number;
	inTransition: boolean;
	ftbActive: boolean;
	audioChannels?: Record<string, AudioChannel>;
	masterLevel: number;
	programPeak: [number, number];
	gainReduction?: number;
	momentaryLufs?: number;
	shortTermLufs?: number;
	integratedLufs?: number;
	tallyState: Record<string, TallyStatus>;
	sources: Record<string, SourceInfo>;
	presets?: PresetInfo[];
	recording?: RecordingStatus;
	srtOutput?: SRTOutputStatus;
	destinations?: DestinationInfo[];
	graphics?: GraphicsState;
	replay?: ReplayState;
	operators?: OperatorInfo[];
	locks?: Record<string, LockInfo>;
	lastChangedBy?: string;
	seq: number;
	timestamp: number;
}

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

export interface EasingConfig {
	type: string;
	x1?: number;
	y1?: number;
	x2?: number;
	y2?: number;
}

export interface GraphicsLayerState {
	id: number;
	template?: string;
	active: boolean;
	fadePosition?: number;
	animationMode?: string;
	animationHz?: number;
	zOrder: number;
	x: number;
	y: number;
	width: number;
	height: number;
}

export interface GraphicsState {
	layers?: GraphicsLayerState[];
}

export interface LayoutState {
	activePreset: string;
	slots: LayoutSlotState[];
}

export interface LayoutSlotState {
	id: number;
	sourceKey: string;
	enabled: boolean;
	x: number;
	y: number;
	width: number;
	height: number;
	zOrder: number;
	animating?: boolean;
	scaleMode?: string;
	cropAnchor?: [number, number];
}

export interface LayoutSlotConfig {
	sourceKey: string;
	rect: { min: { x: number; y: number }; max: { x: number; y: number } };
	zOrder: number;
	enabled: boolean;
	border?: { width: number; colorY: number; colorCb: number; colorCr: number };
	transition?: { type: 'cut' | 'dissolve' | 'fly'; durationMs: number };
}

export interface LayoutConfig {
	preset?: string;
	slots?: LayoutSlotConfig[];
	name?: string;
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

export type MacroAction =
	// Switching
	| 'cut' | 'preview' | 'transition' | 'ftb'
	// Audio
	| 'set_audio' | 'audio_mute' | 'audio_afv' | 'audio_trim' | 'audio_master'
	| 'audio_eq' | 'audio_compressor' | 'audio_delay'
	// Graphics
	| 'graphics_on' | 'graphics_off' | 'graphics_auto_on' | 'graphics_auto_off'
	| 'graphics_add_layer' | 'graphics_remove_layer'
	| 'graphics_set_rect' | 'graphics_set_zorder'
	| 'graphics_fly_in' | 'graphics_fly_out' | 'graphics_slide'
	| 'graphics_animate' | 'graphics_animate_stop'
	| 'graphics_upload_frame'
	// Output
	| 'recording_start' | 'recording_stop'
	// Presets
	| 'preset_recall'
	// Keys
	| 'key_set' | 'key_delete'
	// Source
	| 'source_label' | 'source_delay' | 'source_position'
	// Replay (mark-based)
	| 'replay_mark_in' | 'replay_mark_out' | 'replay_play' | 'replay_stop'
	// Replay (clip-based)
	| 'replay_quick_clip' | 'replay_play_last' | 'replay_play_clip'
	// Timing
	| 'wait'
	// SCTE-35
	| 'scte35_cue' | 'scte35_return' | 'scte35_cancel' | 'scte35_hold' | 'scte35_extend';

export interface MacroStep {
	action: MacroAction;
	params: Record<string, unknown>;
}

export interface Macro {
	name: string;
	steps: MacroStep[];
}

export type MacroStepStatus = 'pending' | 'running' | 'done' | 'failed' | 'skipped';

export interface MacroStepState {
	action: MacroAction;
	summary: string;
	status: MacroStepStatus;
	error?: string;
	waitMs?: number;
	waitStartMs?: number;
}

export interface MacroExecutionState {
	running: boolean;
	macroName: string;
	steps: MacroStepState[];
	currentStep: number;
	error?: string;
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

export interface PipelineFormatInfo {
	width: number;
	height: number;
	fpsNum: number;
	fpsDen: number;
	name: string;
}

export interface SCTE35State {
	enabled: boolean;
	activeEvents: Record<string, SCTE35Active>;
	eventLog: SCTE35Event[];
	heartbeatOk: boolean;
	config: SCTE35Config;
}

export interface SCTE35Active {
	eventId: number;
	commandType: string;
	isOut: boolean;
	durationMs?: number;
	elapsedMs: number;
	remainingMs?: number;
	autoReturn: boolean;
	held: boolean;
	spliceTimePts: number;
	startedAt: number;
	descriptors?: SCTE35DescriptorInfo[];
}

export interface SCTE35DescriptorInfo {
	segEventId: number;
	segmentationType: number;
	upidType: number;
	upid: string;
	durationTicks?: number;
	subSegmentNum?: number;
	subSegmentsExpected?: number;
}

export interface SCTE35Event {
	eventId: number;
	commandType: string;
	isOut: boolean;
	durationMs?: number;
	autoReturn: boolean;
	descriptors?: SCTE35DescriptorInfo[];
	availNum?: number;
	availsExpected?: number;
	spliceTimePts?: number;
	timestamp: number;
	status: string;
	source?: string;
	destinationId?: string;
}

export interface SCTE35Config {
	heartbeatIntervalMs: number;
	defaultPreRollMs: number;
	pid: number;
	verifyEncoding: boolean;
	webhookUrl?: string;
}

export interface SCTE35CueRequest {
	commandType: 'splice_insert' | 'time_signal';
	isOut?: boolean;
	durationMs?: number;
	autoReturn?: boolean;
	preRollMs?: number;
	eventId?: number;
	descriptors?: SCTE35DescriptorRequest[];
}

export interface SCTE35DescriptorRequest {
	segmentationType: number;
	durationMs?: number;
	upidType: number;
	upid: string;
	subSegmentNum?: number;
	subSegmentsExpected?: number;
}

export interface SCTE35Rule {
	id: string;
	name: string;
	enabled: boolean;
	priority?: number;
	conditions?: SCTE35RuleCondition[];
	logic?: 'and' | 'or';
	action: 'pass' | 'delete' | 'replace';
	replaceWith?: Record<string, unknown>;
	destinations?: string[];
}

export interface SCTE35RuleCondition {
	field: string;
	operator: string;
	value: string;
}

export interface ControlRoomState {
	programSource: string;
	previewSource: string;
	transitionType: string;
	transitionDurationMs: number;
	transitionPosition: number;
	transitionEasing?: string;
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
	layout?: LayoutState;
	replay?: ReplayState;
	operators?: OperatorInfo[];
	locks?: Record<string, LockInfo>;
	pipelineFormat?: PipelineFormatInfo;
	scte35?: SCTE35State;
	macro?: MacroExecutionState;
	lastChangedBy?: string;
	seq: number;
	timestamp: number;
}

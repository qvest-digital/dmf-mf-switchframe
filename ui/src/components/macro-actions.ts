import type { MacroAction } from '$lib/api/types';

export type ActionMeta = {
	label: string;
	category: string;
	description: string;
};

export const ACTION_META: Record<MacroAction, ActionMeta> = {
	// Switching
	cut: { label: 'Cut', category: 'Switching', description: 'Switch program to source' },
	preview: { label: 'Preview', category: 'Switching', description: 'Set preview source' },
	transition: { label: 'Transition', category: 'Switching', description: 'Dissolve, wipe, dip, or stinger to source' },
	ftb: { label: 'Fade to Black', category: 'Switching', description: 'Fade to black' },
	// Audio
	set_audio: { label: 'Set Level', category: 'Audio', description: 'Set audio level' },
	audio_mute: { label: 'Mute', category: 'Audio', description: 'Mute/unmute source' },
	audio_afv: { label: 'AFV', category: 'Audio', description: 'Audio-follow-video toggle' },
	audio_trim: { label: 'Trim', category: 'Audio', description: 'Set input trim' },
	audio_master: { label: 'Master', category: 'Audio', description: 'Set master level' },
	audio_eq: { label: 'EQ', category: 'Audio', description: 'Set EQ band' },
	audio_compressor: { label: 'Compressor', category: 'Audio', description: 'Set compressor' },
	audio_delay: { label: 'Audio Delay', category: 'Audio', description: 'Set audio delay (lip-sync)' },
	// Graphics
	graphics_on: { label: 'Graphics On', category: 'Graphics', description: 'Show DSK overlay' },
	graphics_off: { label: 'Graphics Off', category: 'Graphics', description: 'Hide DSK overlay' },
	graphics_auto_on: { label: 'Auto On', category: 'Graphics', description: 'Auto-transition graphics on' },
	graphics_auto_off: { label: 'Auto Off', category: 'Graphics', description: 'Auto-transition graphics off' },
	// Output
	recording_start: { label: 'Start Recording', category: 'Output', description: 'Start recording' },
	recording_stop: { label: 'Stop Recording', category: 'Output', description: 'Stop recording' },
	// Presets
	preset_recall: { label: 'Recall Preset', category: 'Presets', description: 'Recall a saved preset' },
	// Keys
	key_set: { label: 'Set Key', category: 'Keys', description: 'Apply chroma/luma key to source' },
	key_delete: { label: 'Remove Key', category: 'Keys', description: 'Remove key from source' },
	// Source
	source_label: { label: 'Set Label', category: 'Source', description: 'Rename source label' },
	source_delay: { label: 'Source Delay', category: 'Source', description: 'Set source video delay' },
	source_position: { label: 'Set Position', category: 'Source', description: 'Reorder source position' },
	// Replay
	replay_mark_in: { label: 'Mark In', category: 'Replay', description: 'Set replay mark-in point' },
	replay_mark_out: { label: 'Mark Out', category: 'Replay', description: 'Set replay mark-out point' },
	replay_play: { label: 'Play', category: 'Replay', description: 'Play replay clip' },
	replay_stop: { label: 'Stop', category: 'Replay', description: 'Stop replay' },
	replay_quick_clip: { label: 'Quick Clip', category: 'Replay', description: 'Quick clip from buffer' },
	replay_play_last: { label: 'Play Last', category: 'Replay', description: 'Replay last clip' },
	replay_play_clip: { label: 'Play Clip', category: 'Replay', description: 'Play saved clip by ID' },
	// Timing
	wait: { label: 'Wait', category: 'Timing', description: 'Pause between steps' },
	// SCTE-35
	scte35_cue: { label: 'Ad Break Cue', category: 'SCTE-35', description: 'Start an ad break' },
	scte35_return: { label: 'Return', category: 'SCTE-35', description: 'End ad break' },
	scte35_cancel: { label: 'Cancel', category: 'SCTE-35', description: 'Cancel a pending splice' },
	scte35_hold: { label: 'Hold', category: 'SCTE-35', description: 'Hold break indefinitely' },
	scte35_extend: { label: 'Extend', category: 'SCTE-35', description: 'Extend break duration' },
};

export const CATEGORIES = ['Switching', 'Timing', 'Audio', 'Graphics', 'Output', 'Presets', 'Keys', 'Source', 'Replay', 'SCTE-35'] as const;

export const SOURCE_ACTIONS: MacroAction[] = [
	'cut', 'preview', 'transition', 'set_audio',
	'audio_mute', 'audio_afv', 'audio_trim',
	'audio_eq', 'audio_compressor', 'audio_delay',
	'key_set', 'key_delete', 'source_label',
	'source_delay', 'source_position',
	'replay_mark_in', 'replay_mark_out', 'replay_play',
	'replay_quick_clip', 'replay_play_clip',
];

export const WIPE_DIRECTIONS = [
	{ value: 'h-left', label: 'Left' },
	{ value: 'h-right', label: 'Right' },
	{ value: 'v-top', label: 'Top' },
	{ value: 'v-bottom', label: 'Bottom' },
	{ value: 'box-center-out', label: 'Box Out' },
	{ value: 'box-edges-in', label: 'Box In' },
];

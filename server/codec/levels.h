// levels.h: the lowest H.264 and HEVC level a stream fits in.
//
// Levels bound the frame size and the luma sample (H.264: macroblock) rate,
// so the frame size alone does not decide one: 1280x720 fits H.264 level 3.1
// at 30 fps but needs 3.2 at 50, and NVENC refuses to open an encoder whose
// level is below what the stream needs.

#ifndef SWITCHFRAME_CODEC_LEVELS_H
#define SWITCHFRAME_CODEC_LEVELS_H

#include <stdint.h>

// ITU-T H.264 Table A-1: level_idc, MaxFS (macroblocks), MaxMBPS
// (macroblocks per second), from 3.1 up.
static inline int avc_level_for(int width, int height, int fps_num, int fps_den) {
	static const int64_t table[][3] = {
		{31, 3600, 108000},   {32, 5120, 216000},   {40, 8192, 245760},
		{42, 8704, 522240},   {50, 22080, 589824},  {51, 36864, 983040},
		{52, 36864, 2073600}, {60, 139264, 4177920}, {61, 139264, 8355840},
		{62, 139264, 16711680},
	};
	int64_t fs = (int64_t)((width + 15) / 16) * ((height + 15) / 16);
	// Rounded up: a level must cover the rate, not the rate truncated.
	int64_t mbps = (fs * fps_num + fps_den - 1) / fps_den;
	for (unsigned i = 0; i < sizeof(table) / sizeof(table[0]); i++) {
		if (fs <= table[i][1] && mbps <= table[i][2]) return (int)table[i][0];
	}
	return 62;
}

// ITU-T H.265 Table A.8: general_level_idc (30x the level), MaxLumaPs,
// MaxLumaSr, from 3.1 up.
static inline int hevc_level_for(int width, int height, int fps_num, int fps_den) {
	static const int64_t table[][3] = {
		{93, 983040, 33177600},     {120, 2228224, 66846720},
		{123, 2228224, 133693440},  {150, 8912896, 267386880},
		{153, 8912896, 534773760},  {156, 8912896, 1069547520},
		{180, 35651584, 1069547520}, {183, 35651584, 2139095040},
		{186, 35651584, 4278190080LL},
	};
	int64_t ps = (int64_t)width * height;
	int64_t sr = (ps * fps_num + fps_den - 1) / fps_den;
	for (unsigned i = 0; i < sizeof(table) / sizeof(table[0]); i++) {
		if (ps <= table[i][1] && sr <= table[i][2]) return (int)table[i][0];
	}
	return 186;
}

#endif

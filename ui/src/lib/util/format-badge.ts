import type { PipelineFormatInfo, EncoderInfo } from '$lib/api/types';

/**
 * Returns a human-readable codec label for the pipeline format badge.
 * Examples: "H.264", "HEVC"
 */
export function codecLabel(codec: string | undefined): string {
	if (!codec) return 'H.264';
	if (codec === 'h265' || codec === 'hevc') return 'HEVC';
	return 'H.264';
}

/**
 * Returns a human-readable color format label.
 * Examples: "420 8-bit", "422 10-bit"
 */
export function colorFormatLabel(colorFormat: string | undefined): string {
	if (!colorFormat) return '420 8-bit';
	if (colorFormat === 'yuv422p10le') return '422 10-bit';
	if (colorFormat === 'yuv420p') return '420 8-bit';
	// Fallback: try to parse from format string
	if (colorFormat.includes('422') && colorFormat.includes('10')) return '422 10-bit';
	if (colorFormat.includes('422')) return '422 8-bit';
	if (colorFormat.includes('10')) return '420 10-bit';
	return '420 8-bit';
}

/**
 * Returns a combined format badge string.
 * Examples: "H.264 · 420 8-bit", "HEVC · 422 10-bit"
 */
export function formatBadgeText(format: PipelineFormatInfo | undefined): string | null {
	if (!format?.codec) return null;
	return `${codecLabel(format.codec)} · ${colorFormatLabel(format.colorFormat)}`;
}

/**
 * Filters encoder list to only show encoders matching the pipeline codec.
 * If pipeline codec is h265/hevc, show only HEVC encoders.
 * If pipeline codec is h264 (or unset), show only H.264 encoders.
 * Returns all encoders if no codec filter applies or list would be empty.
 */
export function filterEncodersByCodec(
	encoders: EncoderInfo[],
	codec: string | undefined,
): EncoderInfo[] {
	if (!encoders.length) return encoders;

	const isHEVC = codec === 'h265' || codec === 'hevc';

	const hevcPatterns = ['hevc', 'h265', 'x265'];
	const avcPatterns = ['h264', 'x264', 'openh264'];

	const filtered = encoders.filter((enc) => {
		const name = enc.name.toLowerCase();
		if (isHEVC) {
			return hevcPatterns.some((p) => name.includes(p));
		} else {
			return avcPatterns.some((p) => name.includes(p));
		}
	});

	// If filtering leaves nothing, return all encoders (safety net)
	return filtered.length > 0 ? filtered : encoders;
}

/**
 * WebGL-based YUV420 renderer for raw program monitor frames.
 *
 * The server sends raw YUV420 frames on the "program-raw" MoQ track with
 * codec "raw/yuv420". Each frame is:
 *   4 bytes: width  (big-endian uint32)
 *   4 bytes: height (big-endian uint32)
 *   W*H bytes: Y plane
 *   W/2*H/2 bytes: Cb plane
 *   W/2*H/2 bytes: Cr plane
 *
 * The fragment shader performs BT.709 limited-range YUV-to-RGB conversion.
 */

/** Parsed YUV420 frame with separate planes. */
export interface ParsedYUVFrame {
	width: number;
	height: number;
	y: Uint8Array;
	cb: Uint8Array;
	cr: Uint8Array;
}

/** Parse a raw YUV420 frame: 8-byte header (width BE32 + height BE32) + planar data. */
export function parseRawYUVFrame(data: Uint8Array): ParsedYUVFrame | null {
	if (data.length < 8) return null;
	const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
	const width = view.getUint32(0);
	const height = view.getUint32(4);
	if (width === 0 || height === 0) return null;
	const ySize = width * height;
	const cbSize = (width >> 1) * (height >> 1);
	const expectedSize = 8 + ySize + cbSize * 2;
	if (data.length < expectedSize) return null;
	return {
		width,
		height,
		y: data.subarray(8, 8 + ySize),
		cb: data.subarray(8 + ySize, 8 + ySize + cbSize),
		cr: data.subarray(8 + ySize + cbSize, 8 + ySize + cbSize + cbSize),
	};
}

export interface YUVRenderer {
	/** Render a packed raw YUV420 frame (8-byte header + planar data). */
	render(packedFrame: Uint8Array): void;
	/** Release WebGL resources. */
	destroy(): void;
}

// Vertex shader: fullscreen quad with texture coordinates.
const VERTEX_SHADER_SOURCE = `
attribute vec2 a_position;
attribute vec2 a_texCoord;
varying vec2 v_texCoord;
void main() {
    gl_Position = vec4(a_position, 0.0, 1.0);
    v_texCoord = a_texCoord;
}
`;

// Fragment shader: BT.709 limited-range YUV420 to RGB conversion.
const FRAGMENT_SHADER_SOURCE = `
precision mediump float;
varying vec2 v_texCoord;
uniform sampler2D u_textureY;
uniform sampler2D u_textureCb;
uniform sampler2D u_textureCr;
void main() {
    float y = texture2D(u_textureY, v_texCoord).r;
    float cb = texture2D(u_textureCb, v_texCoord).r;
    float cr = texture2D(u_textureCr, v_texCoord).r;
    y = (y - 0.0625) * 1.1644;
    cb = cb - 0.5;
    cr = cr - 0.5;
    float r = y + 1.7927 * cr;
    float g = y - 0.2132 * cb - 0.5329 * cr;
    float b = y + 2.1124 * cb;
    gl_FragColor = vec4(clamp(r, 0.0, 1.0), clamp(g, 0.0, 1.0), clamp(b, 0.0, 1.0), 1.0);
}
`;

function compileShader(gl: WebGLRenderingContext, type: number, source: string): WebGLShader | null {
	const shader = gl.createShader(type);
	if (!shader) return null;
	gl.shaderSource(shader, source);
	gl.compileShader(shader);
	if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
		console.warn('[YUVRenderer] Shader compile error:', gl.getShaderInfoLog(shader));
		gl.deleteShader(shader);
		return null;
	}
	return shader;
}

function createProgram(gl: WebGLRenderingContext, vs: WebGLShader, fs: WebGLShader): WebGLProgram | null {
	const program = gl.createProgram();
	if (!program) return null;
	gl.attachShader(program, vs);
	gl.attachShader(program, fs);
	gl.linkProgram(program);
	if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
		console.warn('[YUVRenderer] Program link error:', gl.getProgramInfoLog(program));
		gl.deleteProgram(program);
		return null;
	}
	return program;
}

function createTexture(gl: WebGLRenderingContext): WebGLTexture | null {
	const tex = gl.createTexture();
	if (!tex) return null;
	gl.bindTexture(gl.TEXTURE_2D, tex);
	gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
	gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
	gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
	gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
	return tex;
}

/**
 * Create a WebGL-based YUV420 renderer for a canvas element.
 * Returns null if WebGL is unavailable.
 */
export function createYUVRenderer(canvas: HTMLCanvasElement): YUVRenderer | null {
	const gl = canvas.getContext('webgl2') || canvas.getContext('webgl');
	if (!gl) return null;

	const vs = compileShader(gl, gl.VERTEX_SHADER, VERTEX_SHADER_SOURCE);
	const fs = compileShader(gl, gl.FRAGMENT_SHADER, FRAGMENT_SHADER_SOURCE);
	if (!vs || !fs) return null;

	const program = createProgram(gl, vs, fs);
	if (!program) return null;

	// Attribute locations
	const aPosition = gl.getAttribLocation(program, 'a_position');
	const aTexCoord = gl.getAttribLocation(program, 'a_texCoord');

	// Uniform locations
	const uTextureY = gl.getUniformLocation(program, 'u_textureY');
	const uTextureCb = gl.getUniformLocation(program, 'u_textureCb');
	const uTextureCr = gl.getUniformLocation(program, 'u_textureCr');

	// Fullscreen quad vertices: position (x,y) + texCoord (u,v)
	// Positions: clip space (-1..1), texCoords: (0..1) with Y flipped
	const vertices = new Float32Array([
		// pos       texCoord
		-1, -1,      0, 1,
		 1, -1,      1, 1,
		-1,  1,      0, 0,
		 1,  1,      1, 0,
	]);

	const vbo = gl.createBuffer();
	gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
	gl.bufferData(gl.ARRAY_BUFFER, vertices, gl.STATIC_DRAW);

	// Create textures for Y, Cb, Cr planes
	const texY = createTexture(gl);
	const texCb = createTexture(gl);
	const texCr = createTexture(gl);

	if (!texY || !texCb || !texCr) return null;

	let currentWidth = 0;
	let currentHeight = 0;
	let destroyed = false;

	function render(packedFrame: Uint8Array): void {
		if (destroyed) return;

		const parsed = parseRawYUVFrame(packedFrame);
		if (!parsed) return;

		const { width, height, y, cb, cr } = parsed;
		const chromaW = width >> 1;
		const chromaH = height >> 1;

		// Track resolution for texture recreation; don't override canvas size
		// (ProgramPreview's ResizeObserver handles HiDPI canvas sizing).
		if (width !== currentWidth || height !== currentHeight) {
			currentWidth = width;
			currentHeight = height;
		}

		// Always set viewport to current canvas backing dimensions.
		gl.viewport(0, 0, canvas.width, canvas.height);

		gl.useProgram(program);

		// Upload Y plane (full resolution, LUMINANCE)
		gl.activeTexture(gl.TEXTURE0);
		gl.bindTexture(gl.TEXTURE_2D, texY);
		gl.texImage2D(gl.TEXTURE_2D, 0, gl.LUMINANCE, width, height, 0, gl.LUMINANCE, gl.UNSIGNED_BYTE, y);
		gl.uniform1i(uTextureY, 0);

		// Upload Cb plane (half resolution)
		gl.activeTexture(gl.TEXTURE1);
		gl.bindTexture(gl.TEXTURE_2D, texCb);
		gl.texImage2D(gl.TEXTURE_2D, 0, gl.LUMINANCE, chromaW, chromaH, 0, gl.LUMINANCE, gl.UNSIGNED_BYTE, cb);
		gl.uniform1i(uTextureCb, 1);

		// Upload Cr plane (half resolution)
		gl.activeTexture(gl.TEXTURE2);
		gl.bindTexture(gl.TEXTURE_2D, texCr);
		gl.texImage2D(gl.TEXTURE_2D, 0, gl.LUMINANCE, chromaW, chromaH, 0, gl.LUMINANCE, gl.UNSIGNED_BYTE, cr);
		gl.uniform1i(uTextureCr, 2);

		// Set up vertex attributes
		gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
		gl.enableVertexAttribArray(aPosition);
		gl.vertexAttribPointer(aPosition, 2, gl.FLOAT, false, 16, 0);
		gl.enableVertexAttribArray(aTexCoord);
		gl.vertexAttribPointer(aTexCoord, 2, gl.FLOAT, false, 16, 8);

		// Draw fullscreen quad
		gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
	}

	function destroy(): void {
		if (destroyed) return;
		destroyed = true;
		gl.deleteTexture(texY);
		gl.deleteTexture(texCb);
		gl.deleteTexture(texCr);
		gl.deleteBuffer(vbo);
		gl.deleteProgram(program);
		gl.deleteShader(vs);
		gl.deleteShader(fs);
	}

	return { render, destroy };
}

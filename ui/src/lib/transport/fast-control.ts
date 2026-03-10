const MSG_LAYOUT_SLOT_POSITION = 0x01;
const MSG_TRANSITION_POSITION = 0x02;

export interface FastControl {
	sendSlotPosition(slotId: number, x: number, y: number, w: number, h: number): void;
	sendTransitionPosition(position: number): void;
	close(): void;
}

export function encodeSlotPosition(
	slotId: number,
	x: number,
	y: number,
	w: number,
	h: number,
): Uint8Array {
	const buf = new ArrayBuffer(10);
	const view = new DataView(buf);
	view.setUint8(0, MSG_LAYOUT_SLOT_POSITION);
	view.setUint8(1, slotId);
	view.setUint16(2, x);
	view.setUint16(4, y);
	view.setUint16(6, w);
	view.setUint16(8, h);
	return new Uint8Array(buf);
}

export function encodeTransitionPosition(position: number): Uint8Array {
	const buf = new ArrayBuffer(5);
	const view = new DataView(buf);
	view.setUint8(0, MSG_TRANSITION_POSITION);
	view.setFloat32(1, position);
	return new Uint8Array(buf);
}

export function createFastControl(transport: WebTransport): FastControl {
	const writer = transport.datagrams.writable.getWriter();

	return {
		sendSlotPosition(slotId: number, x: number, y: number, w: number, h: number) {
			writer.write(encodeSlotPosition(slotId, x, y, w, h)).catch(() => {});
		},

		sendTransitionPosition(position: number) {
			writer.write(encodeTransitionPosition(position)).catch(() => {});
		},

		close() {
			writer.releaseLock();
		},
	};
}

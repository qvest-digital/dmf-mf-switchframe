import type { FastControl } from './fast-control';

/**
 * Call fn on each control, swallowing errors so one dead engine
 * doesn't prevent the remaining engines from receiving the datagram.
 */
function safeCall(controls: FastControl[], fn: (c: FastControl) => void): void {
	for (const c of controls) {
		try { fn(c); } catch { /* engine down, skip */ }
	}
}

/**
 * Creates a FastControl that fans out every call to all provided controls.
 * Used in dual-engine mode to send datagrams to both engines simultaneously.
 */
export function createDualFastControl(controls: FastControl[]): FastControl {
	return {
		sendTransitionPosition(position: number) {
			safeCall(controls, c => c.sendTransitionPosition(position));
		},

		sendGraphicsLayerPosition(layerId: number, x: number, y: number, w: number, h: number) {
			safeCall(controls, c => c.sendGraphicsLayerPosition(layerId, x, y, w, h));
		},

		sendDVESlotTransform(
			slotId: number,
			posX: number,
			posY: number,
			width: number,
			height: number,
			rotation: number,
			opacity: number,
		) {
			safeCall(controls, c =>
				c.sendDVESlotTransform(slotId, posX, posY, width, height, rotation, opacity));
		},

		sendDVESingleParam(slotId: number, paramId: number, value: number) {
			safeCall(controls, c => c.sendDVESingleParam(slotId, paramId, value));
		},

		sendDVESlotSource(slotId: number, sourceKey: string) {
			safeCall(controls, c => c.sendDVESlotSource(slotId, sourceKey));
		},

		sendDVESlotEnable(slotId: number, enabled: boolean) {
			safeCall(controls, c => c.sendDVESlotEnable(slotId, enabled));
		},

		sendDVEScaleMode(slotId: number, mode: number, anchorX: number, anchorY: number) {
			safeCall(controls, c => c.sendDVEScaleMode(slotId, mode, anchorX, anchorY));
		},

		sendDVEBorder(
			slotId: number,
			width: number,
			radius: number,
			colorY: number,
			colorCb: number,
			colorCr: number,
		) {
			safeCall(controls, c => c.sendDVEBorder(slotId, width, radius, colorY, colorCb, colorCr));
		},

		writeDatagram(data: Uint8Array) {
			safeCall(controls, c => c.writeDatagram(data));
		},

		commitTransitionPosition(position: number) {
			for (const c of controls) {
				try { c.commitTransitionPosition?.(position); } catch { /* engine down */ }
			}
		},

		commitDVESlotTransform(slotId: number, posX: number, posY: number, width: number, height: number, rotation: number, opacity: number) {
			for (const c of controls) {
				try { c.commitDVESlotTransform?.(slotId, posX, posY, width, height, rotation, opacity); } catch { /* engine down */ }
			}
		},

		close() {
			safeCall(controls, c => c.close());
		},
	};
}

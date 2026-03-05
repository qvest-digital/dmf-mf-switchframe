export type NotificationType = 'error' | 'warning' | 'info';

export interface Notification {
	id: number;
	type: NotificationType;
	message: string;
	timestamp: number;
	dismissed: boolean;
}

let notifications = $state<Notification[]>([]);
let nextId = 0;
const dismissTimers = new Map<number, ReturnType<typeof setTimeout>>();

export function notify(type: NotificationType, message: string): void {
	const id = nextId++;
	const notification: Notification = {
		id,
		type,
		message,
		timestamp: Date.now(),
		dismissed: false,
	};
	notifications.push(notification);

	// Auto-dismiss warnings and info after 5 seconds
	if (type === 'warning' || type === 'info') {
		const timer = setTimeout(() => {
			dismiss(id);
			dismissTimers.delete(id);
		}, 5000);
		dismissTimers.set(id, timer);
	}
}

export function dismiss(id: number): void {
	const exists = notifications.some((n) => n.id === id && !n.dismissed);
	if (exists) {
		// Clear auto-dismiss timer if one exists
		const timer = dismissTimers.get(id);
		if (timer) {
			clearTimeout(timer);
			dismissTimers.delete(id);
		}
		// Immutable update: mark as dismissed
		notifications = notifications.map((n) => (n.id === id ? { ...n, dismissed: true } : n));
		// Remove from array after animation time (300ms)
		setTimeout(() => {
			notifications = notifications.filter((n) => n.id !== id);
		}, 300);
	}
}

export function getNotifications(): Notification[] {
	return notifications.filter((n) => !n.dismissed);
}

export function clearAll(): void {
	for (const timer of dismissTimers.values()) clearTimeout(timer);
	dismissTimers.clear();
	notifications = [];
}

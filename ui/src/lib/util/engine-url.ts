/**
 * Infer Engine B URL from Engine A's hostname using DNS naming convention.
 * Returns null if hostname doesn't match the expected pattern.
 */
export function inferEngineBUrl(hostname: string, protocol: string = 'https:'): string | null {
  const parts = hostname.split('.');
  if (parts.length < 3) return null;
  // Reject bare IPv4 addresses (all parts are numeric)
  if (parts.every(p => /^\d+$/.test(p))) return null;
  if (parts[0].endsWith('-b')) return null;
  const engineBHost = `${parts[0]}-b.${parts.slice(1).join('.')}`;
  return `${protocol}//${engineBHost}`;
}

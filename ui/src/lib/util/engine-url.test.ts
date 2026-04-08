import { describe, it, expect } from 'vitest';
import { inferEngineBUrl } from './engine-url';

describe('inferEngineBUrl', () => {
  it('infers engine B from standard engine A hostname', () => {
    expect(inferEngineBUrl('mysession.engines.switchframe.dev'))
      .toBe('https://mysession-b.engines.switchframe.dev');
  });

  it('returns null for localhost', () => {
    expect(inferEngineBUrl('localhost')).toBeNull();
  });

  it('returns null for bare IP address', () => {
    expect(inferEngineBUrl('192.168.1.1')).toBeNull();
  });

  it('returns null when already on engine B', () => {
    expect(inferEngineBUrl('mysession-b.engines.switchframe.dev')).toBeNull();
  });

  it('preserves http protocol', () => {
    expect(inferEngineBUrl('mysession.engines.switchframe.dev', 'http:'))
      .toBe('http://mysession-b.engines.switchframe.dev');
  });

  it('handles custom zones', () => {
    expect(inferEngineBUrl('sess1.custom.zone.example.com'))
      .toBe('https://sess1-b.custom.zone.example.com');
  });
});

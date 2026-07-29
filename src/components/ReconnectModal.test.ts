import { describe, expect, it } from 'vitest';
import { ReauthRequiredError } from '../lib/auth';
import { reconnectError } from './ReconnectModal';

describe('reconnectError', () => {
  it('names a rejected credential as such', () => {
    // The two failures need different responses — type a different token, or
    // wait for the server — so they must not share one message.
    expect(reconnectError(new ReauthRequiredError())).toContain(
      'did not accept that token',
    );
  });

  it('reports anything else as the server being unreachable', () => {
    expect(reconnectError(new TypeError('Failed to fetch'))).toContain(
      'could not reach the server',
    );
    expect(reconnectError(new Error('GET /api/labels failed: 500'))).toContain(
      'could not reach the server',
    );
    // A non-Error rejection must still produce a sentence, not "undefined".
    expect(reconnectError('boom')).toContain('could not reach the server');
  });
});

import { describe, expect, it } from 'vitest';
import { diagnose } from './AuthReturn';

describe('diagnose', () => {
  it('flags a query-string code as a Web-platform registration', () => {
    expect(diagnose('?code=abc&state=xyz', '')).toEqual({ kind: 'web-platform' });
  });

  it('treats a fragment code as the normal pending handshake', () => {
    expect(diagnose('', '#code=abc&state=xyz')).toEqual({
      kind: 'pending',
      where: 'fragment',
    });
  });

  it('surfaces an Entra error with its description', () => {
    expect(
      diagnose('', '#error=access_denied&error_description=User+cancelled&state=x'),
    ).toEqual({ kind: 'error', detail: 'access_denied: User cancelled' });
  });

  it('surfaces a query-string error too', () => {
    expect(diagnose('?error=invalid_request&state=x', '')).toEqual({
      kind: 'error',
      detail: 'invalid_request',
    });
  });
});

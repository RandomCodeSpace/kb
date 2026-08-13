import { afterEach, describe, expect, it, vi } from 'vitest';
import { newId, newTask } from './model';

const UUID_SHAPE = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('newId', () => {
  it('uses crypto.randomUUID when the context provides it', () => {
    expect(newId()).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
  });

  it('builds a spec-shaped v4 from getRandomValues outside secure contexts', () => {
    // Plain-HTTP hosts expose crypto without randomUUID.
    const realCrypto = globalThis.crypto;
    vi.stubGlobal('crypto', {
      getRandomValues: (arr: Uint8Array<ArrayBuffer>) => realCrypto.getRandomValues(arr),
    });
    const first = newId();
    const second = newId();
    expect(first).toMatch(UUID_SHAPE);
    expect(second).toMatch(UUID_SHAPE);
    expect(first).not.toBe(second);
    // Tasks stay creatable in that context too.
    expect(newTask({ title: 'insecure context' }).id).toMatch(UUID_SHAPE);
  });
});

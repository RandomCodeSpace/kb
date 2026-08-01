// @vitest-environment jsdom

import { StrictMode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const state = vi.hoisted(() => ({
  redirect: false,
  render: vi.fn(),
  createRoot: vi.fn(),
}));

vi.mock('react-dom/client', () => ({
  createRoot: state.createRoot,
}));

vi.mock('./lib/auth', () => ({
  isAuthRedirect: () => state.redirect,
}));

vi.mock('./App', () => ({
  default: function MockApp() { return null; },
}));

vi.mock('./components/AuthReturn', () => ({
  AuthReturn: function MockAuthReturn() { return null; },
}));

beforeEach(() => {
  vi.resetModules();
  state.redirect = false;
  state.render.mockReset();
  state.createRoot.mockReset();
  state.createRoot.mockReturnValue({ render: state.render });
  document.body.innerHTML = '<div id="root"></div>';
});

describe('application bootstrap', () => {
  it('renders the auth-return bridge for an authentication redirect', async () => {
    state.redirect = true;
    await import('./main');
    expect(state.createRoot).toHaveBeenCalledWith(document.getElementById('root'));
    const element = state.render.mock.calls[0]![0] as { type: { name?: string } };
    expect(element.type.name).toBe('MockAuthReturn');
  });

  it('renders the board application under strict mode for an ordinary load', async () => {
    await import('./main');
    const element = state.render.mock.calls[0]![0] as {
      type: unknown;
      props: { children: { type: { name?: string } } };
    };
    expect(element.type).toBe(StrictMode);
    expect(element.props.children.type.name).toBe('MockApp');
  });
});

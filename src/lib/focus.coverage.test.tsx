// @vitest-environment jsdom

import { useRef } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  dialogFocusables,
  focusWrapIndex,
  restoreFocus,
  useDialogFocus,
} from './focus';

function FocusHarness({ skipWithin }: { skipWithin?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const onKeyDown = useDialogFocus(ref, { skipWithin });
  return (
    <div ref={ref} tabIndex={-1} onKeyDown={onKeyDown}>
      <button type="button">first</button>
      <div className="picker"><button type="button">picker</button></div>
      <button type="button">last</button>
    </div>
  );
}

function EmptyFocusHarness() {
  const ref = useRef<HTMLDivElement>(null);
  const onKeyDown = useDialogFocus(ref);
  return <div ref={ref} tabIndex={-1} onKeyDown={onKeyDown}>empty</div>;
}

function NullRefHarness() {
  const ref = useRef<HTMLDivElement>(null);
  const onKeyDown = useDialogFocus(ref);
  return <button type="button" onKeyDown={onKeyDown}>outside</button>;
}

function AutoFocusHarness() {
  const ref = useRef<HTMLDivElement>(null);
  const onKeyDown = useDialogFocus(ref);
  return <div ref={ref} onKeyDown={onKeyDown}><button type="button" autoFocus>inside</button></div>;
}

describe('DOM focus helpers', () => {
  it('returns no controls for a missing root', () => {
    expect(dialogFocusables(null)).toEqual([]);
  });

  it('filters hidden, inert and negative-tab-index controls', () => {
    const root = document.createElement('div');
    root.innerHTML = `
      <button id="ok">ok</button>
      <div inert><button id="inert">inert</button></div>
      <button id="hidden" hidden>hidden</button>
      <button id="parked" tabindex="-1">parked</button>`;
    expect(dialogFocusables(root).map((node) => node.id)).toEqual(['ok']);
  });

  it('restores focus to a remounted card with the same task id', () => {
    const detached = document.createElement('button');
    detached.dataset.task = 'task-1';
    const replacement = document.createElement('button');
    replacement.dataset.task = 'task-1';
    document.body.append(replacement);
    const focus = vi.spyOn(replacement, 'focus');
    restoreFocus(detached);
    expect(focus).toHaveBeenCalledOnce();
  });

  it('restores focus directly when the original trigger remains attached', () => {
    const trigger = document.createElement('button');
    document.body.append(trigger);
    const focus = vi.spyOn(trigger, 'focus');
    restoreFocus(trigger);
    expect(focus).toHaveBeenCalledOnce();
  });

  it('leaves focus alone when a detached trigger has no durable task id', () => {
    const detached = document.createElement('button');
    expect(() => restoreFocus(detached)).not.toThrow();
    expect(document.activeElement).not.toBe(detached);
  });

  it('leaves focus alone when no remounted task matches the trigger', () => {
    const detached = document.createElement('button');
    detached.dataset.task = 'missing';
    expect(() => restoreFocus(detached)).not.toThrow();
  });

  it('does nothing for a non-HTML trigger', () => {
    expect(() => restoreFocus(document.createElementNS('http://www.w3.org/2000/svg', 'svg')))
      .not.toThrow();
  });

  it('moves initial focus into the dialog and wraps Tab in both directions', () => {
    render(<FocusHarness />);
    const first = screen.getByRole('button', { name: 'first' });
    const last = screen.getByRole('button', { name: 'last' });
    expect(document.activeElement).toBe(first);
    last.focus();
    fireEvent.keyDown(last, { key: 'Tab' });
    expect(document.activeElement).toBe(first);
    fireEvent.keyDown(first, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  it('leaves Tab handling to an explicitly skipped widget', () => {
    render(<FocusHarness skipWithin=".picker" />);
    const picker = screen.getByRole('button', { name: 'picker' });
    picker.focus();
    const event = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    picker.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(document.activeElement).toBe(picker);
  });

  it('ignores keys other than Tab', () => {
    render(<FocusHarness />);
    const first = screen.getByRole('button', { name: 'first' });
    const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    first.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
    expect(document.activeElement).toBe(first);
  });

  it('does not consume Tab when the dialog has no focusable controls', () => {
    render(<EmptyFocusHarness />);
    const empty = screen.getByText('empty');
    const event = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    empty.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(false);
  });

  it('tolerates a dialog ref that never mounts', () => {
    render(<NullRefHarness />);
    expect(screen.getByRole('button', { name: 'outside' })).toBeInTheDocument();
  });

  it('preserves focus already claimed inside the dialog', () => {
    render(<AutoFocusHarness />);
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'inside' }));
  });

  it('returns no wrap target for an empty ring', () => {
    expect(focusWrapIndex(0, 0, false)).toBe(-1);
  });

  it('enters at the correct edge when current focus is outside the ring', () => {
    expect(focusWrapIndex(3, -1, false)).toBe(0);
    expect(focusWrapIndex(3, 3, true)).toBe(2);
  });
});

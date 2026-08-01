// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ConfirmDialog } from './ConfirmDialog';

describe('ConfirmDialog DOM', () => {
  it('validates input, confirms by Enter, and restores opener focus', async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    const opener = document.body.appendChild(document.createElement('button'));
    opener.focus();
    const { unmount } = render(<ConfirmDialog title="Delete?" body="Permanent" inputLabel="Reason" inputRequired destructive onConfirm={onConfirm} onClose={onClose} />);
    const input = screen.getByRole('textbox', { name: 'Reason' });
    expect(document.activeElement).toBe(input);
    expect((screen.getByRole('button', { name: 'OK' }) as HTMLButtonElement).disabled).toBe(true);
    await user.type(input, ' because{Enter}');
    expect(onConfirm).toHaveBeenCalledWith(' because');
    expect(onClose).toHaveBeenCalledTimes(1);
    unmount();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it('supports secondary, Escape, backdrop cancel, acknowledgement, and Tab wrap', async () => {
    const user = userEvent.setup();
    const close = vi.fn();
    const secondary = vi.fn();
    const { rerender } = render(<ConfirmDialog title="Choose" secondaryLabel="Archive" onSecondary={secondary} onClose={close} />);
    await user.click(screen.getByRole('button', { name: 'Archive' }));
    expect(secondary).toHaveBeenCalled();
    expect(close).toHaveBeenCalled();
    rerender(<ConfirmDialog title="Notice" onClose={close} />);
    const ok = screen.getByRole('button', { name: 'OK' });
    ok.focus();
    await user.keyboard('{Escape}');
    expect(close).toHaveBeenCalledTimes(2);
    await user.click(ok);
    expect(close).toHaveBeenCalledTimes(3);
    const backdrop = screen.getByRole('alertdialog').parentElement!;
    await user.pointer({ target: backdrop, keys: '[MouseLeft]' });
    expect(close).toHaveBeenCalledTimes(4);
  });

  it('traps Tab in both directions and leaves button Enter to the button', async () => {
    const user = userEvent.setup();
    const close = vi.fn(), confirm = vi.fn();
    render(<ConfirmDialog title="Trap" inputLabel="Value" onConfirm={confirm} onClose={close} />);
    const input = screen.getByRole('textbox', { name: 'Value' });
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    const ok = screen.getByRole('button', { name: 'OK' });
    input.focus();
    await user.tab();
    expect(document.activeElement).toBe(cancel);
    ok.focus();
    await user.tab();
    expect(document.activeElement).toBe(input);
    input.focus();
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(ok);
    fireEvent.keyDown(ok, { key: 'Enter' });
    expect(confirm).not.toHaveBeenCalled();
  });

  it('refuses Enter while required input is blank', () => {
    const confirm = vi.fn(), close = vi.fn();
    render(<ConfirmDialog title="Required" inputLabel="Value" inputRequired onConfirm={confirm} onClose={close} />);
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    expect(confirm).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
  });
});

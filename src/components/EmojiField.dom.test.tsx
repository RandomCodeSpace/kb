// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { EmojiField } from './EmojiField';

vi.mock('@emoji-mart/data', () => ({ default: { offline: true } }));
vi.mock('@emoji-mart/react', () => ({
  default: (props: { onEmojiSelect: (e: { native?: string }) => void }) => (
    <div><button onClick={() => props.onEmojiSelect({ native: '🔧' })}>pick valid</button><button onClick={() => props.onEmojiSelect({ native: '👨‍💻' })}>pick invalid</button><button onClick={() => props.onEmojiSelect({})}>pick missing</button></div>
  ),
}));

describe('EmojiField DOM', () => {
  it('loads the offline picker, applies and removes a supported emoji', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(<EmojiField inputId="emoji" value="" onChange={onChange} />);
    const trigger = screen.getByRole('button', { name: 'Choose an emoji' });
    await user.click(trigger);
    expect(screen.getByText('loading…')).toBeTruthy();
    await user.click(await screen.findByRole('button', { name: 'pick valid' }));
    expect(onChange).toHaveBeenCalledWith('🔧');
    expect(document.activeElement).toBe(trigger);
    rerender(<EmojiField inputId="emoji" value="🔧" onChange={onChange} />);
    await user.click(screen.getByRole('button', { name: /Emoji:/ }));
    await user.click(await screen.findByRole('button', { name: 'Remove emoji' }));
    expect(onChange).toHaveBeenCalledWith('');
  });

  it('explains unsupported picks and closes on Escape', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<EmojiField inputId="emoji2" value="" onChange={onChange} />);
    const trigger = screen.getByRole('button', { name: 'Choose an emoji' });
    await user.click(trigger);
    await user.click(await screen.findByRole('button', { name: 'pick invalid' }));
    expect(screen.getByRole('alert').textContent).toContain('not supported');
    expect(onChange).not.toHaveBeenCalled();
    await user.click(trigger);
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(document.activeElement).toBe(trigger);
  });

  it('closes on an outside pointer press without stealing focus back', async () => {
    const user = userEvent.setup();
    render(<><EmojiField inputId="emoji3" value="" onChange={vi.fn()} /><button>outside</button></>);
    const trigger = screen.getByRole('button', { name: 'Choose an emoji' });
    await user.click(trigger);
    await screen.findByRole('dialog');
    const outside = screen.getByRole('button', { name: 'outside' });
    outside.focus();
    fireEvent.pointerDown(outside);
    fireEvent.click(trigger);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).not.toBe(trigger);
  });

  it('ignores unrelated keys and treats a picker result without native text as removal', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<EmojiField inputId="emoji4" value="" onChange={onChange} />);
    await user.click(screen.getByRole('button', { name: 'Choose an emoji' }));
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'x' }));
    await user.click(await screen.findByRole('button', { name: 'pick missing' }));
    expect(onChange).toHaveBeenCalledWith('');
  });
});

// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@emoji-mart/react', () => { throw new Error('chunk unavailable'); });
vi.mock('@emoji-mart/data', () => ({ default: {} }));

describe('EmojiField picker failure DOM', () => {
  it('shows a deterministic optional-field fallback when the picker chunk fails', async () => {
    const { EmojiField } = await import('./EmojiField');
    const user = userEvent.setup();
    render(<EmojiField inputId="emoji-fail" value="" onChange={vi.fn()} />);
    await user.click(screen.getByRole('button', { name: 'Choose an emoji' }));
    expect(await screen.findByText(/picker unavailable/)).toBeTruthy();
  });
});

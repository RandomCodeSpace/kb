// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { DateField, dayLabel, monthGrid, monthLabel } from './DateField';

describe('DateField DOM', () => {
  it('opens at the current value, pages, keyboard-navigates, picks, and restores focus', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<DateField inputId="due" value="2026-07-27" onChange={onChange} />);
    const trigger = screen.getByRole('button', { name: /Change due date/ });
    await user.click(trigger);
    expect(screen.getByRole('dialog', { name: 'Choose a due date' })).toBeTruthy();
    const selected = screen.getByRole('button', { name: 'Monday 27 July 2026' });
    expect(document.activeElement).toBe(selected);
    await user.keyboard('{ArrowRight}{Enter}');
    expect(onChange).toHaveBeenCalledWith('2026-07-28');
    expect(document.activeElement).toBe(trigger);

    await user.click(trigger);
    await user.click(screen.getByRole('button', { name: 'Next month' }));
    expect(screen.getByText('August 2026')).toBeTruthy();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('supports native input, clear, today, toggle, and outside close', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<><DateField inputId="due" value="2026-07-27" onChange={onChange} /><button>outside</button></>);
    await user.clear(document.querySelector('#due') as HTMLInputElement);
    expect(onChange).toHaveBeenCalledWith('');
    const trigger = screen.getByRole('button', { name: /Change due date/ });
    await user.click(trigger);
    await user.click(screen.getByRole('button', { name: 'Clear' }));
    expect(onChange).toHaveBeenCalledWith('');
    await user.click(trigger);
    await user.click(screen.getByRole('button', { name: 'Today' }));
    expect(onChange).toHaveBeenCalledWith(expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/));
    await user.click(trigger);
    await user.pointer({ target: screen.getByRole('button', { name: 'outside' }), keys: '[MouseLeft>]' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('falls back from invalid values, pages by keyboard both ways, and toggles the trigger', async () => {
    expect(monthGrid('invalid')).toHaveLength(42);
    expect(monthLabel('invalid')).toBe('');
    expect(dayLabel('invalid')).toBe('invalid');
    const user = userEvent.setup();
    render(<DateField inputId="bad-date" value="invalid" onChange={vi.fn()} />);
    const trigger = screen.getByRole('button', { name: /Change due date, invalid/ });
    await user.click(trigger);
    const active = document.activeElement as HTMLElement;
    const initialMonth = screen.getByText(/\d{4}$/).textContent;
    await user.keyboard('{PageUp}');
    expect(screen.getByText(/\d{4}$/).textContent).not.toBe(initialMonth);
    await user.keyboard('{PageDown}');
    expect(screen.getByText(initialMonth!)).toBeTruthy();
    fireEvent.keyDown(active, { key: 'x' });
    await user.click(trigger);
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

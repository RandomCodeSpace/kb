// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { LabelsCombobox } from './LabelsCombobox';

describe('LabelsCombobox DOM', () => {
  it('adds suggestions and free text, removes chips, and supports backspace', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const { rerender } = render(
      <LabelsCombobox inputId="labels" value={[]} suggestions={['env::prod', 'backend']} onChange={onChange} />,
    );
    const input = screen.getByRole('combobox');
    await user.click(input);
    expect(screen.getByRole('listbox').getAttribute('style')).toBeNull();
    await user.keyboard('{ArrowDown}{Enter}');
    expect(onChange).toHaveBeenLastCalledWith(['backend']);

    rerender(<LabelsCombobox inputId="labels" value={['env::prod']} suggestions={['env::prod', 'backend']} onChange={onChange} />);
    await user.type(input, 'custom{Enter}');
    expect(onChange).toHaveBeenLastCalledWith(['env::prod', 'custom']);
    await user.clear(input);
    await user.keyboard('{Backspace}');
    expect(onChange).toHaveBeenLastCalledWith([]);
    await user.click(screen.getByRole('button', { name: 'Remove env::prod' }));
    expect(onChange).toHaveBeenLastCalledWith([]);
    expect(document.activeElement).toBe(input);
  });

  it('navigates, closes on Escape, and keeps an empty dropdown mounted but hidden', async () => {
    const user = userEvent.setup();
    render(<LabelsCombobox inputId="labels" value={[]} suggestions={['alpha', 'beta']} onChange={vi.fn()} />);
    const input = screen.getByRole('combobox');
    await user.type(input, 'zzz');
    expect(screen.getByRole('listbox', { hidden: true }).style.visibility).toBe('hidden');
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    await user.clear(input);
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowUp}');
    expect(input.getAttribute('aria-activedescendant')).toMatch(/-1$/);
  });

  it('opens from a closed keyboard state and supports pointer selection and chip-container focus', async () => {
    const onChange = vi.fn();
    const { container, rerender } = render(<LabelsCombobox inputId="labels3" value={['plain']} suggestions={['alpha', 'beta']} onChange={onChange} />);
    const input = screen.getByRole('combobox');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(screen.getByRole('listbox')).toBeTruthy();
    const beta = screen.getByRole('option', { name: 'beta' });
    fireEvent.mouseEnter(beta);
    expect(input.getAttribute('aria-activedescendant')).toMatch(/-1$/);
    fireEvent.pointerDown(beta);
    expect(onChange).toHaveBeenLastCalledWith(['plain', 'beta']);
    fireEvent.pointerDown(container.querySelector('.labelchips')!);
    expect(document.activeElement).toBe(input);
    rerender(<LabelsCombobox inputId="labels3" value={['plain']} suggestions={[]} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'Remove plain' }));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  it('leaves closed no-op keys and duplicate free-text labels unchanged', () => {
    const onChange = vi.fn();
    render(
      <LabelsCombobox
        inputId="labels-noop"
        value={['alpha']}
        suggestions={['alpha']}
        onChange={onChange}
      />,
    );
    const input = screen.getByRole('combobox');

    fireEvent.keyDown(input, { key: 'ArrowUp' });
    fireEvent.keyDown(input, { key: 'Escape' });
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.change(input, { target: { value: 'alpha' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(onChange).not.toHaveBeenCalled();
  });
});

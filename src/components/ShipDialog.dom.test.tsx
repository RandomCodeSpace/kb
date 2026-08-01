// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ShipDialog } from './ShipDialog';

describe('ShipDialog DOM', () => {
  it('exposes both warnings and all actions', async () => {
    const user = userEvent.setup();
    const onShip = vi.fn(), onTickAll = vi.fn(), onCancel = vi.fn();
    render(<ShipDialog warning={{ open: 2, total: 3, blocked: true }} title="Release" onShip={onShip} onTickAll={onTickAll} onCancel={onCancel} />);
    expect(screen.getByText('2 of 3 checklist items are still open.')).toBeTruthy();
    expect(screen.getByText('This card is flagged blocked.')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Tick everything' }));
    await user.click(screen.getByRole('button', { name: 'Ship anyway' }));
    await user.keyboard('{Escape}');
    expect(onTickAll).toHaveBeenCalledTimes(1);
    expect(onShip).toHaveBeenCalledTimes(1);
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('hides tick-all when nothing is open and cancels on backdrop', async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<ShipDialog warning={{ open: 0, total: 1, blocked: true }} title="Release" onShip={vi.fn()} onTickAll={vi.fn()} onCancel={onCancel} />);
    expect(screen.queryByRole('button', { name: 'Tick everything' })).toBeNull();
    await user.pointer({ target: screen.getByRole('alertdialog').parentElement!, keys: '[MouseLeft]' });
    expect(onCancel).toHaveBeenCalled();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'x' }));
  });
});

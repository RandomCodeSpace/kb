import { test, expect } from '@playwright/test';

test('display settings update the real board, preserve focus and survive reload', async ({ page, request, baseURL }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  for (const task of [
    { title: 'High priority display task', prio: 1 },
    { title: 'Low priority display task', prio: 3 },
  ]) {
    const response = await request.post('/api/tasks', {
      headers: { Origin: baseURL },
      data: { ...task, project: 'display-check', desc: 'Display description' },
    });
    expect(response.status()).toBe(201);
  }
  await page.goto('/#/p/display-check');
  await expect(page.locator('#board .card')).toHaveCount(2);
  await page.keyboard.press('d');
  const panel = page.getByRole('dialog', { name: 'Display options', exact: true });
  const compact = panel.getByRole('button', { name: 'Compact', exact: true });
  await compact.focus();
  await page.keyboard.press('Space');
  await expect(compact).toBeFocused();
  await expect(compact).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('html')).toHaveAttribute('data-density', 'compact');

  await panel.getByRole('checkbox', { name: 'Description', exact: true }).uncheck();
  await expect(page.locator('#board')).not.toContainText('Display description');
  await panel.getByRole('checkbox', { name: 'Show cancelled column' }).check();
  await expect(page.locator('.col[data-status="cancelled"]')).toBeVisible();
  await panel.getByRole('checkbox', { name: 'Hide empty columns' }).check();
  await expect(page.locator('.col[data-status="cancelled"]')).toBeHidden();
  await expect(page.locator('.col[data-status="doing"]')).toBeHidden();
  await panel.getByRole('combobox', { name: 'Column sort' }).click();
  await page.getByRole('option', { name: 'Priority', exact: true }).click();
  await expect(page.locator('#board .card').first()).toContainText('High priority display task');

  const limit = panel.getByLabel('WIP limit for Todo');
  await limit.fill('1');
  await limit.press('Tab');
  await expect(page.locator('.col[data-status="todo"]')).toHaveClass(/is-over/);
  await limit.fill('-2');
  await limit.press('Tab');
  await expect(limit).toHaveAttribute('aria-invalid', 'true');
  await expect(panel.getByRole('alert')).toHaveText('Use a positive whole number.');
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('kb-web-settings')).wip.todo)).toBe(1);
  await limit.fill('2');
  await limit.press('Tab');
  await expect(page.locator('.col[data-status="todo"]')).not.toHaveClass(/is-over/);
  await page.keyboard.press('Escape');
  await expect(panel).toBeHidden();
  await expect(page.getByRole('button', { name: 'Display options', exact: true })).toBeFocused();

  await page.reload();
  await page.getByRole('button', { name: 'Display options', exact: true }).click();
  await expect(compact).toHaveAttribute('aria-pressed', 'true');
  await expect(panel.getByRole('checkbox', { name: 'Description', exact: true })).not.toBeChecked();
  await expect(panel.getByRole('combobox', { name: 'Column sort' })).toHaveText('Priority');
  await expect(limit).toHaveValue('2');
  await limit.fill('');
  await limit.press('Tab');
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('kb-web-settings')).wip.todo)).toBeUndefined();
  // Commands and Display share the same settings, even with the panel open.
  await page.keyboard.press('Control+k');
  await page.locator('#palette-search').fill('Density: comfortable');
  await expect(page.locator('#palette-list').getByRole('option')).toHaveCount(1);
  await page.locator('#palette-search').press('Enter');
  await expect(panel.getByRole('button', { name: 'Comfortable', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#palette-dialog')).toBeHidden();
  await panel.getByRole('checkbox', { name: 'Description', exact: true }).check();
  await expect(page.locator('html')).not.toHaveAttribute('data-density', 'compact');
  await panel.getByRole('button', { name: 'Close display options' }).click();
  await expect(panel).toBeHidden();
  expect(errors).toEqual([]);
});

test('display fits a narrow viewport and keeps every option reachable', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Display options', exact: true }).click();
  const panel = page.getByRole('dialog', { name: 'Display options', exact: true });
  const cancelled = panel.getByLabel('WIP limit for Cancelled');
  await cancelled.fill('4');
  await cancelled.press('Tab');
  await expect(cancelled).toBeInViewport();
  const geometry = await panel.evaluate(element => {
    const rect = element.getBoundingClientRect();
    return { left: rect.left, right: rect.right, bottom: rect.bottom, width: innerWidth, height: innerHeight, overflow: element.scrollWidth > element.clientWidth };
  });
  expect(geometry.left).toBeGreaterThanOrEqual(0);
  expect(geometry.right).toBeLessThanOrEqual(geometry.width);
  expect(geometry.bottom).toBeLessThanOrEqual(geometry.height);
  expect(geometry.overflow).toBe(false);
  await panel.getByRole('button', { name: 'Close display options' }).click();
  await expect(panel).toBeHidden();
});

import { test, expect } from '@playwright/test';

// Feature flows on the real embedded UI, API and store. Each test works in its
// own project so the disposable board is shared safely across tests.
async function seed(request, baseURL, project, tasks) {
  const out = [];
  for (const task of tasks) {
    const response = await request.post('/api/tasks', { headers: { Origin: baseURL }, data: { project, ...task } });
    expect(response.status()).toBe(201);
    out.push(await response.json());
  }
  return out;
}
const card = (page, seq) => page.locator(`#board .card[data-seq="${seq}"]`);
const column = (page, status) => page.locator(`#board .col[data-status="${status}"]`);
const moved = (page) => page.waitForResponse((r) => r.url().endsWith('/move') && r.request().method() === 'POST');

test('drag and drop, keyboard lift and bulk selection move cards', async ({ page, request, baseURL }) => {
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const [a, b, c] = await seed(request, baseURL, 'moves', [{ title: 'Drag me' }, { title: 'Lift me' }, { title: 'Select me' }]);
  await page.goto('/#/p/moves');
  await expect(column(page, 'todo').locator('.card')).toHaveCount(3);

  let done = moved(page);
  await card(page, a.seq).dragTo(column(page, 'doing').locator('.col-body'));
  expect((await done).ok()).toBeTruthy();
  await expect(column(page, 'doing').locator('.card')).toHaveText([/Drag me/]);

  await card(page, b.seq).focus();
  await page.keyboard.press('Space');
  await expect(card(page, b.seq)).toHaveClass(/is-lifted/);
  await page.keyboard.press('l');
  await expect(column(page, 'doing').locator('.card')).toHaveCount(2);
  done = moved(page);
  await page.keyboard.press('Space');
  expect((await done).ok()).toBeTruthy();
  await expect(card(page, b.seq)).not.toHaveClass(/is-lifted/);
  await expect(page.locator('.toast').filter({ hasText: 'Moved #' + b.seq + ' to Doing' })).toBeVisible();

  await card(page, c.seq).focus();
  await page.keyboard.press('v');
  await expect(card(page, c.seq)).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('l');
  await page.keyboard.press('v');
  const bar = page.getByRole('toolbar', { name: 'Selection actions' });
  await expect(bar).toContainText('2 selected');
  await bar.getByRole('combobox', { name: 'Move selection to column' }).click();
  done = moved(page);
  await page.getByRole('option', { name: 'Done', exact: true }).click();
  expect((await done).ok()).toBeTruthy();
  await expect(column(page, 'done').locator('.card')).toHaveCount(2);
  await expect(bar).toBeHidden();
  expect(errors).toEqual([]);
});

test('search, filters, palette and the column composer', async ({ page, request, baseURL }) => {
  await seed(request, baseURL, 'find', [{ title: 'Tagged card', tags: ['ui'] }, { title: 'Plain card' }]);
  await page.goto('/#/p/find');
  await expect(column(page, 'todo').locator('.card')).toHaveCount(2);

  await page.keyboard.press('/');
  const search = page.getByRole('dialog', { name: 'Search tasks' });
  await search.locator('#search').fill('tag:ui');
  await expect(column(page, 'todo').locator('.card')).toHaveText([/Tagged card/]);
  await expect(search).toContainText('1 of 2');
  await page.keyboard.press('Escape');
  await expect(search).toBeHidden();
  const filters = page.getByRole('region', { name: 'Search and filters' });
  await expect(filters).toContainText('tag:ui');
  await filters.getByRole('button', { name: 'Clear' }).click();
  await expect(column(page, 'todo').locator('.card')).toHaveCount(2);

  await filters.getByRole('button', { name: 'Unlabelled' }).click();
  await expect(column(page, 'todo').locator('.card')).toHaveText([/Plain card/]);
  await page.keyboard.press('X');
  await expect(column(page, 'todo').locator('.card')).toHaveCount(2);

  await page.keyboard.press('Control+k');
  await page.locator('#palette-search').fill('hide empty');
  await expect(page.locator('#palette-list').getByRole('option')).toHaveCount(1);
  await page.keyboard.press('Enter');
  await expect(column(page, 'done')).toBeHidden();
  await page.keyboard.press('Control+k');
  await page.locator('#palette-search').fill('show empty');
  await page.keyboard.press('Enter');
  await expect(column(page, 'done')).toBeVisible();

  await page.keyboard.press('?');
  await expect(page.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeVisible();
  await page.keyboard.press('Escape');

  await column(page, 'doing').getByRole('button', { name: 'Add task to Doing' }).click();
  const composer = page.getByRole('textbox', { name: 'New task in Doing' });
  await composer.fill('Quick one !high #ui ~M');
  await expect(column(page, 'doing').locator('.composer-open')).toContainText('High');
  const created = page.waitForResponse((r) => r.url().endsWith('/api/tasks') && r.request().method() === 'POST');
  await composer.press('Enter');
  expect((await created).status()).toBe(201);
  const quick = column(page, 'doing').locator('.card').filter({ hasText: 'Quick one' });
  await expect(quick).toHaveAttribute('data-prio', '1');
  await expect(quick.locator('.chip[data-tag="ui"]')).toBeVisible();
  await expect(composer).toBeFocused();
});

test('task details edit labels, checklist, comments and title', async ({ page, request, baseURL }) => {
  const [t] = await seed(request, baseURL, 'detail', [{ title: 'Detailed card', tags: ['ui'] }]);
  await page.goto('/#/p/detail');
  await card(page, t.seq).click();
  const detail = page.locator('#detail-dialog');
  await expect(detail.getByRole('radiogroup', { name: 'Status' }).getByRole('radio', { name: 'Todo' })).toHaveAttribute('aria-checked', 'true');

  const labels = detail.getByRole('combobox', { name: 'Add label…' });
  await labels.fill('type::bug');
  await page.getByRole('option', { name: 'Create type bug' }).click();
  await expect(detail.locator('.label-row .chip[data-tag="type::bug"]')).toBeVisible();
  await expect(card(page, t.seq).locator('.chip[data-tag="type::bug"]')).toBeVisible();

  await detail.getByRole('textbox', { name: 'New checklist item' }).fill('write the test');
  await page.keyboard.press('Enter');
  await expect(detail.locator('.check-row')).toHaveText([/write the test/]);
  await detail.getByRole('checkbox', { name: 'write the test' }).check();
  await expect(detail.getByRole('progressbar', { name: 'Checklist progress' })).toHaveAttribute('aria-valuenow', '1');

  await detail.getByRole('button', { name: 'Add a comment' }).click();
  await detail.getByRole('textbox', { name: 'New comment' }).fill('First **comment**');
  await page.keyboard.press('Control+Enter');
  await expect(detail.locator('.comment strong')).toHaveText('comment');
  await expect(card(page, t.seq).locator('.chip', { hasText: '1' }).first()).toBeVisible();

  await detail.locator('.title-edit').click();
  await detail.getByRole('textbox', { name: 'Title', exact: true }).fill('Renamed card');
  await page.keyboard.press('Enter');
  await expect(detail.locator('#detail-title')).toHaveText('Renamed card');
  await expect(card(page, t.seq)).toHaveAttribute('aria-label', `#${t.seq} Renamed card`);

  await detail.getByRole('button', { name: 'Edit description' }).click();
  await detail.getByRole('textbox', { name: 'Description' }).fill('Some *body*');
  await page.keyboard.press('Control+Enter');
  await expect(detail.locator('.desc-box em')).toHaveText('body');
  await page.keyboard.press('Escape');
  await expect(detail).toBeHidden();
});

test('cancelling from a card asks for a reason and the toast undoes it', async ({ page, request, baseURL }) => {
  const [t] = await seed(request, baseURL, 'cancel', [{ title: 'Doomed card' }]);
  await page.goto('/#/p/cancel');
  await card(page, t.seq).hover();
  await card(page, t.seq).getByRole('button', { name: 'Cancel task' }).click();
  const ask = page.getByRole('dialog', { name: `Cancel #${t.seq}?` });
  await ask.getByRole('textbox', { name: 'Reason' }).fill('duplicate');
  await ask.getByRole('button', { name: 'Cancel card' }).click();
  await expect(ask).toBeHidden();
  await expect(column(page, 'todo').locator('.card')).toHaveCount(0);
  const toast = page.locator('.toast').filter({ hasText: `Cancelled #${t.seq}` });
  await toast.getByRole('button', { name: 'Undo' }).click();
  await expect(card(page, t.seq)).toBeVisible();
  await expect(column(page, 'todo').locator('.card')).toHaveCount(1);
});

test('settings save an AI endpoint and keep the key write-only', async ({ page }) => {
  await page.goto('/');
  await page.keyboard.press('s');
  const settings = page.getByRole('dialog', { name: 'Settings' });
  await settings.getByRole('textbox', { name: 'Base URL' }).first().fill('http://127.0.0.1:1/v1');
  await settings.getByRole('textbox', { name: 'Model' }).fill('test-model');
  await settings.getByRole('textbox', { name: 'API key' }).fill('sk-test');
  await settings.getByRole('button', { name: 'Save AI settings' }).click();
  await expect(settings.getByRole('status').filter({ hasText: 'AI settings saved' })).toBeVisible();
  await expect(settings.locator('.secret')).toContainText('Saved');
  await settings.getByRole('button', { name: 'Close' }).last().click();
  await page.reload();
  await page.keyboard.press('n');
  const editor = page.getByRole('dialog', { name: 'New task' });
  await expect(editor.getByRole('textbox', { name: 'AI draft prompt' })).toBeVisible();
  await page.keyboard.press('Escape');
  await page.keyboard.press('s');
  await expect(settings.locator('.ai-state')).toContainText('test-model');
  await expect(settings.locator('.secret')).toContainText('Saved');
});

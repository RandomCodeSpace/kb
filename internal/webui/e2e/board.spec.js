import { test, expect } from '@playwright/test';

// These use the real embedded assets, API, SQLite store and EventSource stream.
test('load board, create and move a task, and update a second browser over SSE', async ({ page, context }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const observer = await context.newPage();
  observer.on('pageerror', error => errors.push(error.message));
  await observer.addInitScript(() => {
    const NativeEventSource = window.EventSource;
    window.smokeEvents = [];
    window.EventSource = class extends NativeEventSource {
      constructor(...args) {
        super(...args);
        for (const type of ['hello', 'change']) this.addEventListener(type, () => window.smokeEvents.push(type));
      }
    };
  });
  await observer.goto('/');
  await expect.poll(() => observer.evaluate(() => window.smokeEvents)).toContain('hello');
  await page.goto('/');
  await expect(page.locator('#board .col')).toHaveCount(4);
  await page.getByRole('button', { name: 'New task', exact: true }).click();
  const editor = page.getByRole('dialog', { name: 'New task' });
  await editor.getByRole('textbox', { name: 'Title', exact: true }).fill('Browser smoke task');
  const created = page.waitForResponse(response => response.url().endsWith('/api/tasks') && response.request().method() === 'POST');
  await editor.getByRole('button', { name: 'Save', exact: true }).click();
  expect((await created).status()).toBe(201);
  const todo = page.locator('.col[data-status="todo"] .card').filter({ hasText: 'Browser smoke task' });
  await expect(todo).toBeVisible();
  await expect(observer.locator('.col[data-status="todo"] .card').filter({ hasText: 'Browser smoke task' })).toBeVisible({ timeout: 4000 });
  await expect.poll(() => observer.evaluate(() => window.smokeEvents)).toContain('change');
  await observer.evaluate(() => { window.smokeEvents = []; });

  await todo.click();
  const detail = page.locator('#detail-dialog');
  const moved = page.waitForResponse(response => response.url().endsWith('/move') && response.request().method() === 'POST');
  await detail.getByRole('group', { name: 'Status', exact: true }).getByRole('button', { name: 'Doing', exact: true }).click();
  expect((await moved).ok()).toBeTruthy();
  await expect(observer.locator('.col[data-status="doing"] .card').filter({ hasText: 'Browser smoke task' })).toBeVisible({ timeout: 4000 });
  await expect(observer.locator('.col[data-status="todo"] .card')).toHaveCount(0);
  await expect.poll(() => observer.evaluate(() => window.smokeEvents)).toContain('change');
  await observer.reload();
  await expect(observer.locator('.col[data-status="doing"] .card').filter({ hasText: 'Browser smoke task' })).toBeVisible();
  expect(errors).toEqual([]);
});

test('Markdown keeps image labels without fetching remote images in cards, details or previews', async ({ page }) => {
  const imageRequests = [];
  await page.route('https://tracking.invalid/**', route => {
    imageRequests.push(route.request().url());
    return route.abort();
  });
  await page.goto('/');
  await page.getByRole('button', { name: 'New task', exact: true }).click();
  const editor = page.getByRole('dialog', { name: 'New task' });
  await editor.getByRole('textbox', { name: 'Title', exact: true }).fill('Image privacy regression');
  await editor.getByRole('textbox', { name: 'Description', exact: true }).fill(
    '**Readable description**\n\n![Markdown image](https://tracking.invalid/markdown.png)\n\n<img src="https://tracking.invalid/raw.png" alt="HTML image">\n\n[Safe link](https://example.com)'
  );
  await editor.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect(editor.locator('.editor-preview .img-alt')).toHaveText(['Markdown image', 'HTML image']);
  await expect(editor.locator('.editor-preview img')).toHaveCount(0);
  await editor.getByRole('button', { name: 'Save', exact: true }).click();
  const card = page.locator('.card').filter({ hasText: 'Image privacy regression' });
  await expect(card).toBeVisible();
  await expect(card.locator('img')).toHaveCount(0);
  await card.click();
  const description = page.locator('#detail-dialog .desc-box');
  await expect(description.locator('.img-alt')).toHaveText(['Markdown image', 'HTML image']);
  await expect(description.locator('img')).toHaveCount(0);
  await expect(description.locator('strong')).toHaveText('Readable description');
  await expect(description.getByRole('link', { name: 'Safe link' })).toHaveAttribute('href', 'https://example.com');
  expect(imageRequests).toEqual([]);
});

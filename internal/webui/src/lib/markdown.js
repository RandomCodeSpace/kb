// Markdown to sanitized HTML. marked and DOMPurify are the vendored globals
// loaded by index.html (see static/VENDOR.md). One sanitizer for every surface.
import { iconSvg } from './icons';

/* global marked, DOMPurify */
const MARKED_OPTS = { gfm: true, breaks: true, async: false };
const MD_INLINE_TAGS = ['strong', 'em', 'b', 'i', 'del', 's', 'code', 'a', 'span', 'br', 'kbd', 'sub', 'sup'];
const MD_BLOCK_TAGS = MD_INLINE_TAGS.concat(['p', 'pre', 'ul', 'ol', 'li', 'input', 'blockquote', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'img', 'div']);
const PURIFY_BASE = { ALLOWED_ATTR: ['href', 'title', 'alt', 'src', 'type', 'checked', 'disabled', 'align', 'start'], ALLOW_DATA_ATTR: false, KEEP_CONTENT: true };
const PURIFY_BLOCK = Object.assign({ ALLOWED_TAGS: MD_BLOCK_TAGS }, PURIFY_BASE);
const PURIFY_INLINE = Object.assign({ ALLOWED_TAGS: MD_INLINE_TAGS }, PURIFY_BASE);
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  const tag = node.tagName;
  if (tag === 'A') {
    const href = (node.getAttribute('href') || '').trim();
    if (!/^(https?:|mailto:)/i.test(href)) { node.removeAttribute('href'); return; }
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  } else if (tag === 'IMG') {
    if (!/^https?:/i.test((node.getAttribute('src') || '').trim())) node.parentNode && node.parentNode.removeChild(node);
    else node.setAttribute('loading', 'lazy');
  } else if (tag === 'INPUT') {
    if ((node.getAttribute('type') || '').toLowerCase() !== 'checkbox') { node.parentNode && node.parentNode.removeChild(node); return; }
    node.setAttribute('disabled', '');
    node.setAttribute('tabindex', '-1');
    node.className = 'cb';
  }
});

const cache = new Map();
// renderHTML(md, mode: 'block' | 'inline' | 'card') -> sanitized HTML string,
// with task lists, image placeholders, card headings and copy buttons applied.
export function renderHTML(md, mode = 'block') {
  const text = String(md || '').replace(/\r\n?/g, '\n');
  if (!text.trim()) return '';
  const key = mode + '\u0000' + text;
  const hit = cache.get(key);
  if (hit !== undefined) return hit;
  let html;
  if (mode === 'inline') html = DOMPurify.sanitize(marked.parseInline(text, MARKED_OPTS), PURIFY_INLINE);
  else {
    const root = document.createElement('template');
    root.innerHTML = DOMPurify.sanitize(marked.parse(text, MARKED_OPTS), PURIFY_BLOCK);
    const frag = root.content;
    for (const box of frag.querySelectorAll('input[type="checkbox"]')) {
      const li = box.parentElement;
      if (!li || li.tagName !== 'LI' || box !== li.firstElementChild) continue;
      li.classList.add('task');
      if (box.checked || box.hasAttribute('checked')) li.classList.add('done');
      const body = document.createElement('div');
      body.className = 'task-text min-w-0 flex-1';
      while (box.nextSibling) body.append(box.nextSibling);
      li.append(body);
    }
    // Keep image labels without loading remote resources on any Markdown surface.
    for (const img of frag.querySelectorAll('img')) {
      const alt = document.createElement('span');
      alt.className = 'img-alt';
      alt.innerHTML = iconSvg('image', 12);
      const label = document.createElement('span');
      label.textContent = img.getAttribute('alt') || 'image';
      alt.append(label);
      img.replaceWith(alt);
    }
    if (mode === 'card') {
      for (const h of frag.querySelectorAll('h1,h2,h3,h4,h5,h6')) {
        const p = document.createElement('p');
        const strong = document.createElement('strong');
        strong.append(...h.childNodes);
        p.append(strong);
        h.replaceWith(p);
      }
    } else {
      for (const pre of frag.querySelectorAll('pre')) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'btn btn-ghost btn-xs copy';
        btn.setAttribute('aria-label', 'Copy code');
        btn.dataset.copy = '';
        btn.innerHTML = iconSvg('copy', 12);
        pre.append(btn);
      }
    }
    html = root.innerHTML;
  }
  if (cache.size > 500) cache.clear();
  cache.set(key, html);
  return html;
}

import type { Effort, Prio } from './model';

export interface StoryDraft {
  title: string;
  emoji: string;
  desc: string;
  prio: Prio;
  due: string;
  effort: Effort | '';
  tags: string[];
  checks: { text: string; done: boolean }[];
}

const DUE_RE = /^\d{4}-\d{2}-\d{2}$/;
const EMOJI_RE = /^\p{Extended_Pictographic}(?:️)?/u;
const CONTROL_RE = /[\u0000-\u001f\u007f-\u009f\u2028\u2029]/g;

function oneLine(value: string): string {
  return value.replace(CONTROL_RE, ' ').replace(/\s+/g, ' ').trim();
}

function multiLine(value: string): string {
  return value
    .split(/\r\n|\r|\n/)
    .map((line) => line.replace(CONTROL_RE, ' ').replace(/[^\S\n]+/g, ' ').trim())
    .join('\n')
    .trim();
}

export function coerceStoryDraft(body: unknown): StoryDraft {
  const fields = (typeof body === 'object' && body !== null ? body : {}) as Record<string, unknown>;
  const prioNumber = typeof fields.prio === 'number' ? Math.round(fields.prio) : 3;
  const prio = (prioNumber >= 1 && prioNumber <= 4 ? prioNumber : 3) as Prio;
  const effort = fields.effort === 'S' || fields.effort === 'M' || fields.effort === 'L' ? fields.effort : '';
  const due = typeof fields.due === 'string' && DUE_RE.test(fields.due) ? fields.due : '';
  const tags = Array.isArray(fields.tags)
    ? fields.tags
        .filter((tag): tag is string => typeof tag === 'string')
        .map((tag) => oneLine(tag).replace(/^#+/, ''))
        .filter((tag) => tag !== '' && !/\s/.test(tag))
    : [];
  const checks = Array.isArray(fields.checks)
    ? fields.checks.flatMap((check): { text: string; done: boolean }[] => {
        if (typeof check !== 'object' || check === null) return [];
        const item = check as Record<string, unknown>;
        if (typeof item.text !== 'string') return [];
        const text = oneLine(item.text);
        return text === '' ? [] : [{ text, done: item.done === true }];
      })
    : [];
  return {
    title: typeof fields.title === 'string' ? oneLine(fields.title) : '',
    emoji:
      typeof fields.emoji === 'string'
        ? (fields.emoji.trim().match(EMOJI_RE)?.[0] ?? '')
        : '',
    desc: typeof fields.desc === 'string' ? multiLine(fields.desc) : '',
    prio,
    due,
    effort,
    tags,
    checks,
  };
}

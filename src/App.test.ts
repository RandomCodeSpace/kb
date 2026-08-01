import { describe, expect, it } from 'vitest';
import {
  LOCAL_PERSISTENCE_NOTICE,
  mergeConflictNotice,
  transitionNotice,
} from './App';

describe('composite persistence notices', () => {
  it('preserves first-seen order, announces once, and re-arms after dismissal', () => {
    const conflict = mergeConflictNotice(['board title'])!;
    const recovery = 'This board was recovered from older local data.';
    const outbox = 'Metadata delivery is blocked: quota exceeded';
    const announcements: string[] = [];
    let notice: string | null = null;
    const report = (message: string) => {
      const transition = transitionNotice(notice, { type: 'report', message });
      notice = transition.notice;
      if (transition.announcement) announcements.push(transition.announcement);
    };

    report(conflict);
    report(LOCAL_PERSISTENCE_NOTICE);
    report(recovery);
    report(outbox);
    report(conflict);
    report(LOCAL_PERSISTENCE_NOTICE);

    expect(notice).toBe([
      conflict,
      LOCAL_PERSISTENCE_NOTICE,
      recovery,
      outbox,
    ].join('\n\n'));
    expect(announcements).toEqual([
      conflict,
      LOCAL_PERSISTENCE_NOTICE,
      recovery,
      outbox,
    ]);

    const dismissed = transitionNotice(notice, { type: 'dismiss' });
    expect(dismissed).toEqual({ notice: null, announcement: null });
    notice = dismissed.notice;
    report(conflict);
    expect(notice).toBe(conflict);
    expect(announcements.at(-1)).toBe(conflict);
    expect(announcements.filter((message) => message === conflict)).toHaveLength(2);
  });

  it('keeps simultaneous merge and local-persistence warnings exactly once', () => {
    const conflict = mergeConflictNotice(['board title'])!;
    let transition = transitionNotice(null, { type: 'report', message: conflict });
    transition = transitionNotice(transition.notice, {
      type: 'report', message: LOCAL_PERSISTENCE_NOTICE,
    });
    transition = transitionNotice(transition.notice, { type: 'report', message: conflict });
    transition = transitionNotice(transition.notice, {
      type: 'report', message: LOCAL_PERSISTENCE_NOTICE,
    });

    expect(transition.notice).toBe(`${conflict}\n\n${LOCAL_PERSISTENCE_NOTICE}`);
    expect(transition.notice!.split(conflict)).toHaveLength(2);
    expect(transition.notice!.split(LOCAL_PERSISTENCE_NOTICE)).toHaveLength(2);
    expect(transition.announcement).toBeNull();
  });
});

import { describe, expect, it } from 'vitest';
import {
  addStoriesNotice,
  CARD_GONE_NOTICE,
  failureDetail,
  mutationNotice,
  NO_SERVER_NOTICE,
  readNotice,
  transitionNotice,
} from './App';
import { ReauthRequiredError } from './lib/auth';

describe('failureDetail', () => {
  it('names an expired session, which arrives with no body of its own', () => {
    expect(failureDetail(new ReauthRequiredError())).toBe('the session expired');
    expect(failureDetail(new Error(' store: no '))).toBe('store: no');
    expect(failureDetail('network down')).toBe('');
  });
});

describe('mutationNotice', () => {
  it('keeps the server’s own words for a refusal', () => {
    expect(mutationNotice(new Error('store: invalid effort "XL" (want S, M, or L)')))
      .toBe(
        'That change was not saved: store: invalid effort "XL" (want S, M, or L). '
        + 'The board below is what the server has.',
      );
  });

  it('stays a whole sentence when the failure carries no message', () => {
    expect(mutationNotice('network down')).toBe(
      'That change was not saved. The board below is what the server has.',
    );
    expect(mutationNotice(new Error('  '))).toBe(
      'That change was not saved. The board below is what the server has.',
    );
  });

  it('does not vouch for the board when it could not be read back', () => {
    // The claim is only true when a refetch followed the failure. Without one
    // the board is the last confirmed state, and saying otherwise is a lie
    // told at the exact moment the user is deciding whether to redo the work.
    expect(mutationNotice(new Error('network down'), false)).toBe(
      'That change was not saved: network down. The board below is the last one '
      + 'the server confirmed, so it may be out of date.',
    );
    expect(mutationNotice(new ReauthRequiredError(), false)).toBe(
      'That change was not saved: the session expired. The board below is the '
      + 'last one the server confirmed, so it may be out of date.',
    );
  });
});

describe('readNotice', () => {
  it('reports a failed read without claiming a change was lost', () => {
    expect(readNotice(new Error('storage error'))).toBe(
      'The board could not be read from the server: storage error. '
      + 'What you see may be out of date.',
    );
    expect(readNotice('offline')).toBe(
      'The board could not be read from the server. What you see may be out of date.',
    );
  });
});

describe('addStoriesNotice', () => {
  it('names the drafts that were not created, and says the rest were', () => {
    expect(addStoriesNotice(['Rate limiting'], new Error('store: no'))).toBe(
      'One card was not created: store: no. Not saved: Rate limiting. '
      + 'Every other card in that batch was.',
    );
    expect(addStoriesNotice(['Rate limiting', 'Audit log'], 'unknown')).toBe(
      '2 cards were not created. Not saved: Rate limiting; Audit log. '
      + 'Every other card in that batch was.',
    );
  });
});

describe('composite notices', () => {
  it('preserves first-seen order, announces once, and re-arms after dismissal', () => {
    const refusal = mutationNotice(new Error('title must not be empty'));
    const announcements: string[] = [];
    let notice: string | null = null;
    const report = (message: string) => {
      const transition = transitionNotice(notice, { type: 'report', message });
      notice = transition.notice;
      if (transition.announcement) announcements.push(transition.announcement);
    };

    report(refusal);
    report(NO_SERVER_NOTICE);
    report(CARD_GONE_NOTICE);
    report(refusal);
    report(NO_SERVER_NOTICE);

    expect(notice).toBe(
      [refusal, NO_SERVER_NOTICE, CARD_GONE_NOTICE].join('\n\n'),
    );
    expect(announcements).toEqual([refusal, NO_SERVER_NOTICE, CARD_GONE_NOTICE]);

    const dismissed = transitionNotice(notice, { type: 'dismiss' });
    expect(dismissed).toEqual({ notice: null, announcement: null });
    notice = dismissed.notice;
    report(refusal);
    expect(notice).toBe(refusal);
    expect(announcements.at(-1)).toBe(refusal);
    expect(announcements.filter((message) => message === refusal)).toHaveLength(2);
  });

  it('keeps two simultaneous warnings exactly once each', () => {
    let transition = transitionNotice(null, {
      type: 'report', message: NO_SERVER_NOTICE,
    });
    transition = transitionNotice(transition.notice, {
      type: 'report', message: CARD_GONE_NOTICE,
    });
    transition = transitionNotice(transition.notice, {
      type: 'report', message: NO_SERVER_NOTICE,
    });
    transition = transitionNotice(transition.notice, {
      type: 'report', message: CARD_GONE_NOTICE,
    });

    expect(transition.notice).toBe(`${NO_SERVER_NOTICE}\n\n${CARD_GONE_NOTICE}`);
    expect(transition.notice!.split(NO_SERVER_NOTICE)).toHaveLength(2);
    expect(transition.notice!.split(CARD_GONE_NOTICE)).toHaveLength(2);
    expect(transition.announcement).toBeNull();
  });
});

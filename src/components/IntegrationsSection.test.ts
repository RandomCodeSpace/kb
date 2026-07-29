import { describe, expect, it } from 'vitest';
import {
  integrationNameConflict,
  integrationStatus,
  removeDecision,
} from './IntegrationsSection';

describe('integrationNameConflict', () => {
  const rows = [
    { key: 'source:work-gitlab', name: 'work-gitlab' },
    { key: 'draft:1', name: 'personal-github' },
  ];

  it('compares trimmed names case-insensitively', () => {
    expect(
      integrationNameConflict(rows, 'draft:2', '  WORK-GITLAB  '),
    ).toBe(true);
  });

  it('excludes the row being edited and ignores empty candidates', () => {
    expect(
      integrationNameConflict(rows, 'source:work-gitlab', ' WORK-GITLAB '),
    ).toBe(false);
    expect(integrationNameConflict(rows, 'draft:2', '   ')).toBe(false);
  });
});

describe('integrationStatus', () => {
  it('keeps every successful operation distinguishable and non-urgent', () => {
    expect(integrationStatus('work-gitlab', 'test', 'ok')).toEqual({
      action: 'test',
      kind: 'ok',
      key: 'work-gitlab:test:ok:connection ok',
      msg: 'connection ok',
      role: 'status',
    });
    expect(integrationStatus('work-gitlab', 'save', 'ok')).toEqual({
      action: 'save',
      kind: 'ok',
      key: 'work-gitlab:save:ok:saved',
      msg: 'saved',
      role: 'status',
    });
    expect(integrationStatus('work-gitlab', 'remove', 'ok')).toEqual({
      action: 'remove',
      kind: 'ok',
      key: 'work-gitlab:remove:ok:removed',
      msg: 'removed',
      role: 'status',
    });
  });

  it('labels failures with the operation and exposes them as alerts', () => {
    expect(
      integrationStatus('work-gitlab', 'test', 'err', 'connection failed'),
    ).toEqual({
      action: 'test',
      kind: 'err',
      key: 'work-gitlab:test:err:test failed — connection failed',
      msg: 'test failed — connection failed',
      role: 'alert',
    });
    expect(
      integrationStatus('work-gitlab', 'save', 'err', 'request rejected'),
    ).toEqual({
      action: 'save',
      kind: 'err',
      key: 'work-gitlab:save:err:save failed — request rejected',
      msg: 'save failed — request rejected',
      role: 'alert',
    });
    expect(
      integrationStatus('work-gitlab', 'remove', 'err', 'request rejected'),
    ).toEqual({
      action: 'remove',
      kind: 'err',
      key: 'work-gitlab:remove:err:remove failed — request rejected',
      msg: 'remove failed — request rejected',
      role: 'alert',
    });
  });

  it('keys the same outcome separately for each source row', () => {
    expect(integrationStatus('work-gitlab', 'save', 'ok').key).toBe(
      'work-gitlab:save:ok:saved',
    );
    expect(integrationStatus('personal-github', 'save', 'ok').key).toBe(
      'personal-github:save:ok:saved',
    );
  });
});

describe('removeDecision', () => {
  it('arms on the first press and confirms only a second press on that row', () => {
    expect(removeDecision(null, 'work-gitlab')).toEqual({
      armed: 'work-gitlab',
      confirm: false,
    });
    expect(removeDecision('work-gitlab', 'work-gitlab')).toEqual({
      armed: null,
      confirm: true,
    });
  });

  it('moves confirmation to a different row without removing either one', () => {
    expect(removeDecision('work-gitlab', 'personal-github')).toEqual({
      armed: 'personal-github',
      confirm: false,
    });
  });

  it('cancels an armed removal when no row is pressed', () => {
    expect(removeDecision('work-gitlab', null)).toEqual({
      armed: null,
      confirm: false,
    });
  });
});

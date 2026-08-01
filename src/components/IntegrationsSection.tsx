import { useEffect, useId, useRef, useState } from 'react';
import type { Identity } from '../lib/auth';
import type {
  ForgeKind,
  ForgeSource,
  ForgeTestProbe,
  IntegrationPatch,
} from '../lib/api';
import {
  deleteIntegration,
  forgeTest,
  getIntegrations,
  putIntegration,
} from '../lib/api';

export type IntegrationAction = 'test' | 'save' | 'remove';
export type IntegrationStatusKind = 'ok' | 'err';

export interface IntegrationStatus {
  action: IntegrationAction;
  kind: IntegrationStatusKind;
  key: string;
  msg: string;
  role: 'status' | 'alert';
}

const successMessage: Record<IntegrationAction, string> = {
  test: 'connection ok',
  save: 'saved',
  remove: 'removed',
};

/** Keep an upstream message useful without allowing it to expand the row. */
function boundedMessage(message: string | undefined, fallback: string): string {
  const text = message?.trim() ?? '';
  return (text === '' ? fallback : text).slice(0, 200);
}

/** One keyed outcome for one row, so assistive technology sees each update. */
export function integrationStatus(
  row: string,
  action: IntegrationAction,
  kind: IntegrationStatusKind,
  detail?: string,
): IntegrationStatus {
  const msg =
    kind === 'ok'
      ? successMessage[action]
      : `${action} failed — ${boundedMessage(detail, 'request failed')}`;
  return {
    action,
    kind,
    key: `${row}:${action}:${kind}:${msg}`,
    msg,
    role: kind === 'ok' ? 'status' : 'alert',
  };
}

export interface RemoveDecision {
  armed: string | null;
  confirm: boolean;
}

/**
 * Only a second press on the same row confirms removal. Pressing elsewhere
 * moves or clears the pending choice without deleting anything.
 */
export function removeDecision(
  armed: string | null,
  pressed: string | null,
): RemoveDecision {
  if (pressed === null) return { armed: null, confirm: false };
  if (pressed === armed) return { armed: null, confirm: true };
  return { armed: pressed, confirm: false };
}

export interface IntegrationNameRow {
  key: string;
  name: string;
}

/** A source name identifies one server resource after trim/case folding. */
export function integrationNameConflict(
  rows: readonly IntegrationNameRow[],
  ownKey: string,
  candidate: string,
): boolean {
  const canonical = candidate.trim().toLowerCase();
  if (canonical === '') return false;
  return rows.some(
    (row) =>
      row.key !== ownKey && row.name.trim().toLowerCase() === canonical,
  );
}

export interface IntegrationsSectionProps {
  identity: Identity;
  serverPresent: boolean;
}

type RowBusy = IntegrationAction | null;

interface LoadGeneration {
  identity: Identity;
  serverPresent: boolean;
}

interface IntegrationRow {
  generation: LoadGeneration;
  key: string;
  persisted: boolean;
  name: string;
  kind: ForgeKind;
  baseURL: string;
  pat: string;
  hasToken: boolean;
  busy: RowBusy;
  status: IntegrationStatus | null;
}

function sourceRow(
  source: ForgeSource,
  generation: LoadGeneration,
): IntegrationRow {
  return {
    generation,
    key: `source:${source.name}`,
    persisted: true,
    name: source.name,
    kind: source.kind,
    baseURL: source.base_url,
    pat: '',
    hasToken: source.has_token,
    busy: null,
    status: null,
  };
}

function requestError(err: unknown, fallback: string): string {
  return boundedMessage(err instanceof Error ? err.message : undefined, fallback);
}

const busyMessage: Record<IntegrationAction, string> = {
  test: 'testing connection…',
  save: 'saving…',
  remove: 'removing…',
};

/**
 * Forge credentials live only in controlled password inputs. Loaded source
 * records contain a boolean token marker, never the token itself.
 */
export function IntegrationsSection({
  identity,
  serverPresent,
}: Readonly<IntegrationsSectionProps>) {
  const [rows, setRows] = useState<IntegrationRow[]>([]);
  const [loading, setLoading] = useState(serverPresent);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [loadGeneration, setLoadGeneration] = useState<LoadGeneration>(() => ({
    identity,
    serverPresent,
  }));
  const [armedRemove, setArmedRemove] = useState<string | null>(null);
  const draftSequence = useRef(0);
  const operationGeneration = useRef(0);
  const sectionId = useId();

  useEffect(() => {
    let cancelled = false;
    operationGeneration.current += 1;
    const operation = operationGeneration.current;
    const generation: LoadGeneration = { identity, serverPresent };
    setLoadGeneration(generation);
    setArmedRemove(null);
    setLoadError(null);

    if (!serverPresent) {
      setRows([]);
      setLoading(false);
      return () => {
        cancelled = true;
        if (operationGeneration.current === operation) {
          operationGeneration.current += 1;
        }
      };
    }

    // A new load owns a new generation. Results from actions against the old
    // rows can then settle harmlessly without finding a row to mutate.
    setRows([]);
    setLoading(true);
    getIntegrations(identity)
      .then((sources) => {
        if (!cancelled && operationGeneration.current === operation) {
          setRows(sources.map((source) => sourceRow(source, generation)));
        }
      })
      .catch((err: unknown) => {
        if (!cancelled && operationGeneration.current === operation) {
          setRows([]);
          setLoadError(requestError(err, 'could not load integrations'));
        }
      })
      .finally(() => {
        if (!cancelled && operationGeneration.current === operation) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
      if (operationGeneration.current === operation) {
        operationGeneration.current += 1;
      }
    };
  }, [identity, serverPresent]);

  const currentGeneration =
    loadGeneration.identity === identity &&
    loadGeneration.serverPresent === serverPresent
      ? loadGeneration
      : null;
  const visibleRows =
    currentGeneration === null
      ? []
      : rows.filter((row) => row.generation === currentGeneration);
  const currentLoading = currentGeneration === null || loading;
  const currentLoadError = currentGeneration === null ? null : loadError;

  const patchRow = (
    key: string,
    generation: LoadGeneration,
    patch: Partial<IntegrationRow>,
  ) => {
    setRows((current) =>
      current.map((row) =>
        row.key === key && row.generation === generation
          ? { ...row, ...patch }
          : row,
      ),
    );
  };

  const changeRow = (row: IntegrationRow, patch: Partial<IntegrationRow>) => {
    setArmedRemove(null);
    patchRow(row.key, row.generation, { ...patch, status: null });
  };

  const handleTest = async (row: IntegrationRow) => {
    if (row.busy !== null || !row.persisted) return;
    const operation = operationGeneration.current;
    setArmedRemove(null);
    patchRow(row.key, row.generation, { busy: 'test', status: null });

    const probe: ForgeTestProbe = { base_url: row.baseURL.trim() };
    // Blank means "use the stored credential", not "test an empty token".
    if (row.pat !== '') probe.pat = row.pat;

    try {
      const result = await forgeTest(identity, row.name, probe);
      if (operationGeneration.current !== operation) return;
      patchRow(row.key, row.generation, {
        busy: null,
        status: result.ok
          ? integrationStatus(row.name, 'test', 'ok')
          : integrationStatus(
              row.name,
              'test',
              'err',
              boundedMessage(result.error, 'connection failed'),
            ),
      });
    } catch (err) {
      if (operationGeneration.current !== operation) return;
      patchRow(row.key, row.generation, {
        busy: null,
        status: integrationStatus(
          row.name,
          'test',
          'err',
          requestError(err, 'connection failed'),
        ),
      });
    }
  };

  const handleSave = async (row: IntegrationRow) => {
    if (row.busy !== null) return;
    const operation = operationGeneration.current;
    const name = row.name.trim();
    const baseURL = row.baseURL.trim();
    if (name === '' || baseURL === '') return;
    if (integrationNameConflict(visibleRows, row.key, name)) {
      patchRow(row.key, row.generation, {
        status: integrationStatus(
          name,
          'save',
          'err',
          'source name already exists',
        ),
      });
      return;
    }

    setArmedRemove(null);
    patchRow(row.key, row.generation, { busy: 'save', status: null });
    const patch: IntegrationPatch = { kind: row.kind, base_url: baseURL };
    // Blank means "keep the stored credential"; omission preserves it.
    if (row.pat !== '') patch.pat = row.pat;

    try {
      const { tokenCleared } = await putIntegration(identity, name, patch);
      if (operationGeneration.current !== operation) return;
      patchRow(row.key, row.generation, {
        persisted: true,
        name,
        baseURL,
        pat: '',
        hasToken: row.pat !== '' ? true : tokenCleared ? false : row.hasToken,
        busy: null,
        status: integrationStatus(name, 'save', 'ok'),
      });
    } catch (err) {
      if (operationGeneration.current !== operation) return;
      patchRow(row.key, row.generation, {
        busy: null,
        status: integrationStatus(
          name || row.key,
          'save',
          'err',
          requestError(err, 'request rejected'),
        ),
      });
    }
  };

  const handleRemove = async (row: IntegrationRow) => {
    if (row.busy !== null) return;
    const operation = operationGeneration.current;
    const decision = removeDecision(armedRemove, row.key);
    setArmedRemove(decision.armed);
    if (!decision.confirm) return;

    if (!row.persisted) {
      setRows((current) =>
        current.filter(
          (item) =>
            item.key !== row.key || item.generation !== row.generation,
        ),
      );
      return;
    }

    patchRow(row.key, row.generation, { busy: 'remove', status: null });
    try {
      await deleteIntegration(identity, row.name);
      if (operationGeneration.current !== operation) return;
      setRows((current) =>
        current.filter(
          (item) =>
            item.key !== row.key || item.generation !== row.generation,
        ),
      );
    } catch (err) {
      if (operationGeneration.current !== operation) return;
      patchRow(row.key, row.generation, {
        busy: null,
        status: integrationStatus(
          row.name,
          'remove',
          'err',
          requestError(err, 'request rejected'),
        ),
      });
    }
  };

  const addDraft = () => {
    if (
      currentGeneration === null ||
      currentLoading ||
      currentLoadError !== null
    ) {
      return;
    }
    setArmedRemove(null);
    draftSequence.current += 1;
    setRows((current) => [
      ...current,
      {
        generation: currentGeneration,
        key: `draft:${draftSequence.current}`,
        persisted: false,
        name: '',
        kind: 'gitlab',
        baseURL: '',
        pat: '',
        hasToken: false,
        busy: null,
        status: null,
      },
    ]);
  };

  return (
    <section aria-labelledby={`${sectionId}-title`}>
      <h3 id={`${sectionId}-title`}>Integrations</h3>
      {!serverPresent ? (
        <p className="mnote">
          Integrations need the kb server. This board is running from local
          storage, so there are no sources to configure here.
        </p>
      ) : (
        <>
          {currentLoading && (
            <p className="flash busy" role="status">
              Loading integrations…
            </p>
          )}
          {currentLoadError && (
            <p className="flash err" role="alert" title={currentLoadError}>
              {currentLoadError}
            </p>
          )}
          {visibleRows.map((row, index) => {
            const busy = row.busy !== null;
            const failedSave =
              row.status?.kind === 'err' && row.status.action === 'save';
            const rowLabel = row.name.trim() || `new source ${index + 1}`;
            const prefix = `${sectionId}-integration-${index}`;
            const statusId = `${prefix}-status`;

            return (
              <div
                className="irow"
                key={row.key}
                aria-busy={busy || undefined}
              >
                <label htmlFor={`${prefix}-kind`}>Kind</label>
                <select
                  id={`${prefix}-kind`}
                  value={row.kind}
                  disabled={busy || row.persisted}
                  onChange={(event) =>
                    changeRow(row, {
                      kind: event.target.value as ForgeKind,
                    })
                  }
                >
                  <option value="gitlab">GitLab</option>
                  <option value="github">GitHub</option>
                </select>

                <label htmlFor={`${prefix}-name`}>Name</label>
                <input
                  id={`${prefix}-name`}
                  value={row.name}
                  placeholder="work-gitlab"
                  autoComplete="off"
                  disabled={busy || row.persisted}
                  aria-label={`Name for ${rowLabel}`}
                  aria-invalid={failedSave || undefined}
                  aria-describedby={failedSave ? statusId : undefined}
                  onChange={(event) =>
                    changeRow(row, { name: event.target.value })
                  }
                />

                <label htmlFor={`${prefix}-base`}>Base URL</label>
                <input
                  id={`${prefix}-base`}
                  value={row.baseURL}
                  placeholder="gitlab.example.com"
                  autoComplete="off"
                  disabled={busy}
                  aria-invalid={failedSave || undefined}
                  aria-describedby={failedSave ? statusId : undefined}
                  onChange={(event) =>
                    changeRow(row, { baseURL: event.target.value })
                  }
                />

                <label htmlFor={`${prefix}-pat`}>Personal access token</label>
                <input
                  id={`${prefix}-pat`}
                  type="password"
                  value={row.pat}
                  placeholder={
                    row.hasToken
                      ? '••• saved — leave blank to keep'
                      : 'glpat-… / ghp_…'
                  }
                  autoComplete="off"
                  disabled={busy}
                  aria-invalid={failedSave || undefined}
                  aria-describedby={failedSave ? statusId : undefined}
                  onChange={(event) =>
                    changeRow(row, { pat: event.target.value })
                  }
                />

                <button
                  type="button"
                  className="test"
                  disabled={busy || !row.persisted}
                  onClick={() => void handleTest(row)}
                >
                  {row.busy === 'test' ? 'Testing…' : 'Test'}
                </button>
                <button
                  type="button"
                  className="save"
                  disabled={
                    busy ||
                    row.name.trim() === '' ||
                    row.baseURL.trim() === ''
                  }
                  onClick={() => void handleSave(row)}
                >
                  {row.busy === 'save' ? 'Saving…' : 'Save'}
                </button>
                <button
                  type="button"
                  disabled={busy}
                  aria-label={`${
                    armedRemove === row.key ? 'Confirm removal of' : 'Remove'
                  } ${rowLabel}`}
                  onBlur={() =>
                    setArmedRemove((current) =>
                      current === row.key ? null : current,
                    )
                  }
                  onClick={() => void handleRemove(row)}
                >
                  {row.busy === 'remove'
                    ? 'Removing…'
                    : armedRemove === row.key
                      ? 'Confirm remove'
                      : 'Remove'}
                </button>

                <span className="statusline">
                  {row.busy !== null ? (
                    <span
                      key={`${row.key}:${row.busy}:busy`}
                      className="flash busy"
                      role="status"
                    >
                      {busyMessage[row.busy]}
                    </span>
                  ) : row.status !== null ? (
                    <span
                      key={row.status.key}
                      className={`flash ${row.status.kind}`}
                      id={statusId}
                      role={row.status.role}
                      title={row.status.msg}
                    >
                      {row.status.msg}
                    </span>
                  ) : null}
                </span>
              </div>
            );
          })}
          <div className="irow">
            <button
              type="button"
              onClick={addDraft}
              disabled={currentLoading || currentLoadError !== null}
            >
              + Add source
            </button>
          </div>
          <p className="mnote">
            Tokens are stored encrypted on the server and never sent to the
            browser.
          </p>
        </>
      )}
    </section>
  );
}

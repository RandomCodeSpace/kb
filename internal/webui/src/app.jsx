import { useEffect } from 'react';
import { state, settings, useStore, notify, applySettings, STATUSES } from '@/store';
import { Header } from '@/components/header';
import { FiltersBar, Segments, Banner } from '@/components/filters';
import { Board } from '@/components/board';
import { BulkBar } from '@/components/bulk-bar';
import { DetailDialog } from '@/components/detail';
import { EditDialog } from '@/components/edit-dialog';
import { SearchDialog } from '@/components/search-dialog';
import { Palette } from '@/components/palette';
import { HelpDialog } from '@/components/help-dialog';
import { SettingsDialog } from '@/components/settings-dialog';
import { SplitDialog, ImportDialog } from '@/components/wizards';
import { AskDialog } from '@/components/ask-dialog';
import { Toasts, LiveRegion } from '@/components/toasts';
import { TooltipHost } from '@/components/tooltip';
import { wideMQ, phoneMQ } from '@/lib/format';
import { refreshMeta, refreshProjects, refreshTasks, refreshActions, refreshAIStatus, startLive, stopLive } from '@/lib/api';
import { writeProject } from '@/lib/filters';
import { onKeydown, applyRoute } from '@/lib/keys';
import { editDirty } from '@/lib/edit';
import { panelDirty } from '@/lib/detail';
import { settingsDirty } from '@/lib/settings';

async function init() {
  applySettings();
  await Promise.all([refreshMeta(), refreshProjects()]);
  // The route names the project, else the one this browser last chose, else
  // the first on the board — inbox when there is none yet.
  const m = /^#\/p\/(.+)$/.exec(decodeURIComponent(location.hash || ''));
  state.project = m ? m[1] : settings.project || writeProject();
  notify();
  await refreshTasks();
  if (!state.loaded) { state.waiting = true; notify(); }
  applyRoute();
  startLive();
  refreshActions();
  refreshAIStatus();
}

export function App() {
  useStore();
  useEffect(() => {
    init();
    const onVisible = () => { if (document.visibilityState !== 'visible') return; startLive(); refreshTasks(); };
    const onOnline = () => refreshTasks();
    const onUnload = (e) => { if (editDirty() || panelDirty() || settingsDirty()) e.preventDefault(); };
    const onMedia = () => notify();
    const onTouchMove = (e) => { if (state.touch) e.preventDefault(); };
    document.addEventListener('keydown', onKeydown);
    document.addEventListener('visibilitychange', onVisible);
    document.addEventListener('touchmove', onTouchMove, { passive: false });
    window.addEventListener('pagehide', stopLive);
    window.addEventListener('pageshow', startLive);
    window.addEventListener('online', onOnline);
    window.addEventListener('hashchange', applyRoute);
    window.addEventListener('beforeunload', onUnload);
    wideMQ.addEventListener('change', onMedia);
    phoneMQ.addEventListener('change', onMedia);
    return () => {
      document.removeEventListener('keydown', onKeydown);
      document.removeEventListener('visibilitychange', onVisible);
      document.removeEventListener('touchmove', onTouchMove);
      window.removeEventListener('pagehide', stopLive);
      window.removeEventListener('pageshow', startLive);
      window.removeEventListener('online', onOnline);
      window.removeEventListener('hashchange', applyRoute);
      window.removeEventListener('beforeunload', onUnload);
      wideMQ.removeEventListener('change', onMedia);
      phoneMQ.removeEventListener('change', onMedia);
    };
  }, []);
  void STATUSES;
  return (
    <>
      <a className="skip" href="#board">Skip to board</a>
      <Header />
      <FiltersBar />
      <Segments />
      <Banner />
      <div className="flex min-h-0 flex-1"><Board /></div>
      <DetailDialog />
      <BulkBar />
      <SearchDialog />
      <Palette />
      <EditDialog />
      <HelpDialog />
      <SettingsDialog />
      <SplitDialog />
      <ImportDialog />
      <AskDialog />
      <Toasts />
      <LiveRegion />
      <TooltipHost />
    </>
  );
}

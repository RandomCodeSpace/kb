import { cn } from '@/lib/utils';
import { state, ALL_PROJECTS } from '@/store';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem } from '@/components/ui/dropdown-menu';
import { Icon } from '@/components/icon';
import { DisplayPanel } from '@/components/display';
import { isDialogOpen, closeDialog } from '@/lib/dialogs';
import { projectList, projectCount, projectTotal, boardStats, quickById, toggleQuick, activeFilters, setFiltersOpen } from '@/lib/filters';
import { setProject, setProjectMenuOpen, openSearch, openHelp } from '@/lib/keys';
import { openPalette } from '@/lib/palette';
import { openEdit } from '@/lib/edit';
import { openSettings } from '@/lib/settings';

function ProjectMenu() {
  const list = [ALL_PROJECTS, ...projectList()];
  const total = projectTotal();
  return (
    <DropdownMenu open={!!state.projectMenuOpen} onOpenChange={setProjectMenuOpen}>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" id="project-btn" className="max-w-full md:max-w-55" data-tip="Project (p / P)">
          <span className="truncate font-semibold text-fg">{state.project === ALL_PROJECTS ? 'All projects' : state.project || 'No project'}</span>
          <Icon name="chevD" className="text-fg-3" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent id="project-menu" aria-label="Projects">
        <DropdownMenuLabel>Projects</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={state.project} onValueChange={(p) => setProject(p)}>
          {list.map((p) => {
            const all = p === ALL_PROJECTS;
            const count = all ? total : projectCount(p);
            return (
              <DropdownMenuRadioItem key={p} value={p} data-tip={all ? 'Every project on the board' : p}>
                <span className="truncate">{all ? 'All projects' : p}</span>
                <span className="num ml-auto text-11 text-fg-3">{count === null ? '' : String(count)}</span>
              </DropdownMenuRadioItem>
            );
          })}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Stats() {
  const { open, week, blocked } = boardStats();
  const item = (n, label, quick) => (
    <Button key={label} variant="stat" size="sm" aria-pressed={quick ? (state.quick.has(quick) ? 'true' : 'false') : undefined}
      data-tip={quick ? `Filter: ${quickById(quick).label}` : undefined} onClick={quick ? () => toggleQuick(quick) : undefined} disabled={!quick}>
      <span className="num font-medium text-fg">{n}</span>{label}
    </Button>
  );
  return (
    <div id="stats" className="ml-2 hidden items-center gap-1 text-12 text-fg-2 lg:flex" aria-label="Board stats">
      {item(open, 'open')}{item(week, 'this week', 'week')}{item(blocked, 'blocked', 'blocked')}
    </div>
  );
}

export function Header() {
  const active = activeFilters().length;
  const hasQuery = !!state.searchText.trim();
  const version = state.meta.version ? 'v' + String(state.meta.version).replace(/^v/, '') : '';
  return (
    <header className="app-header flex flex-none items-center gap-1 border-b border-line bg-canvas px-2 md:px-3">
      <a href="#/" className="mr-1 flex h-8 items-center gap-2 rounded-md px-1" aria-label="kb board">
        <img src="/logo.svg" className="size-5" width="20" height="20" alt="" />
        <span className="text-13 font-semibold leading-none" aria-hidden="true">kb</span>
      </a>
      <div className="relative min-w-0"><ProjectMenu /></div>
      <Stats />
      <div className="min-w-0 flex-1" />
      <Button variant="ghost" size="icon" id="search-btn" className="relative" data-tip="Search  /" aria-label="Search tasks" aria-haspopup="dialog" aria-controls="search-dialog"
        onClick={() => (isDialogOpen('search') ? closeDialog('search') : openSearch())}>
        <Icon name="search" />
        {hasQuery && <span className="absolute right-1 top-1 size-1.5 rounded-full bg-accent" aria-hidden="true" />}
      </Button>
      <Button variant="ghost" size="icon" className="relative md:hidden" aria-label="Search and filters" aria-expanded={state.filtersOpen ? 'true' : 'false'} aria-controls="filters"
        onClick={() => setFiltersOpen(!state.filtersOpen)}>
        <Icon name="filter" />
        {active > 0 && <span className="absolute -right-0.5 -top-0.5 grid h-4 min-w-4 place-items-center rounded-sm bg-accent px-1 font-mono text-10 font-semibold text-on-accent">{active}</span>}
      </Button>
      <Button variant="ghost" size="icon" data-tip="Commands  Ctrl K" aria-label="Command palette" aria-haspopup="dialog" onClick={openPalette}><Icon name="terminal" /></Button>
      <Button variant="default" className="ml-1" data-tip="New task (n)" onClick={() => openEdit(null)}>
        <Icon name="plus" /><span className="phone-hide">New task</span>
      </Button>
      <span className="mx-1 hidden h-4 w-px bg-line md:block" aria-hidden="true" />
      <DisplayPanel />
      <Button variant="ghost" size="icon" className="hidden md:inline-flex" aria-label="Settings" data-tip="Settings  s" aria-haspopup="dialog" onClick={() => openSettings()}><Icon name="gear" /></Button>
      <Button variant="ghost" size="icon" className="hidden md:inline-flex" data-tip="Keyboard  ?" aria-label="Keyboard help" onClick={openHelp}><Icon name="help" /></Button>
      <span id="conn" className="num ml-1 hidden items-center gap-1.5 pl-1 text-11 text-fg-3 md:flex" data-tip={state.online ? 'Connected' : 'Connection lost'}>
        <span className={cn('size-1.5 rounded-full', state.online ? 'bg-ok' : 'bg-danger')} aria-hidden="true" />
        <span data-tip={state.mode === 'stream' ? 'Live updates: server push' : 'Live updates: polling every 5 seconds'}>{version}</span>
      </span>
    </header>
  );
}

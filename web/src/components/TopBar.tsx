import type { Dispatch, SetStateAction } from 'react';
import type { Route } from '../appShared';
import type { AuthUser, ValidationIssue, Workflow, WorkflowSummary } from '../types';
import { EditorActions } from './topbar/EditorActions';
import { MobileNav } from './topbar/MobileNav';
import { MoreMenu } from './topbar/MoreMenu';
import { PrimaryNav } from './topbar/PrimaryNav';
import { PublishControls } from './topbar/PublishControls';
import { SettingsMenu } from './topbar/SettingsMenu';
import { WorkflowIdentity } from './topbar/WorkflowIdentity';

export interface TopBarProps {
  route: Route;
  navigate: (path: string, options?: { replace?: boolean; force?: boolean }) => boolean | void;
  waitingCount: number;
  workflow: Workflow | null;
  selectOptions: WorkflowSummary[];
  dirty: boolean;
  autosaving: boolean;
  busyNew: boolean;
  switchWorkflow: (id: string, mode: 'view' | 'edit') => void | Promise<void>;
  createBlank: () => void | Promise<void>;
  publishedVersion: number;
  hasDraft: boolean;
  publishing: boolean;
  /** Audit 4E: the live publish phase label; empty when idle. */
  publishPhase?: string;
  saving: boolean;
  online: boolean;
  setVersionsOpen: (open: boolean) => void;
  publish: () => void | Promise<unknown>;
  /** UX Phase 2 D2-6: pending profile-upgrade changes; 0 hides the chip. */
  upgradeCount?: number;
  onUpgradePreview?: () => void;
  /** Operational-trust campaign: opens the workflow egress dialog. */
  onEditEgress: () => void;
  errorCount: number;
  warningCount: number;
  issues: ValidationIssue[];
  setSelectedId: (id: string | null) => void;
  undo: () => void;
  redo: () => void;
  undoStack: Workflow[];
  redoStack: Workflow[];
  principal: AuthUser | null;
  theme: 'light' | 'dark';
  setTheme: Dispatch<SetStateAction<'light' | 'dark'>>;
  setAccountOpen: (open: boolean) => void;
  signOut: () => void | Promise<void>;
  setPaletteOpen: (open: boolean) => void;
  save: () => void | Promise<boolean>;
  startRun: (input?: Record<string, unknown>) => void | Promise<unknown>;
  starting: boolean;
  runBlocked: string | undefined;
}

/** Audit 3D: the top bar reads as three zones instead of one flat row
 *  of controls —
 *    left   brand + primary nav (the mobile Menu under 900px),
 *    centre the editor's workflow identity (select, unsaved state,
 *           new workflow) doubling as the page's single title,
 *    right  primary actions: validation status, undo/redo/Save, then
 *           the always-visible Run, Publish, More cluster, with the
 *           app-level Settings menu at the far edge.
 *
 *  TopBar stays the composition root; each zone lives in ./topbar/. */
export function TopBar(props: TopBarProps) {
  const {
    route,
    navigate,
    waitingCount,
    workflow,
    selectOptions,
    switchWorkflow,
    dirty,
    autosaving,
    busyNew,
    createBlank,
    publishedVersion,
    hasDraft,
    publishing,
    publishPhase,
    saving,
    online,
    setVersionsOpen,
    publish,
    upgradeCount,
    onUpgradePreview,
    onEditEgress,
    errorCount,
    warningCount,
    issues,
    setSelectedId,
    undo,
    redo,
    undoStack,
    redoStack,
    principal,
    theme,
    setTheme,
    setAccountOpen,
    signOut,
    setPaletteOpen,
    save,
    startRun,
    starting,
    runBlocked,
  } = props;

  const isEditor = route.name === 'editor' && workflow !== null;
  const hasRightZone = isEditor || principal?.id === 'anonymous';

  return (
    <header className={`topbar${isEditor ? ' topbar-editor' : ''}`}>
      <div className="topbar-zone topbar-left">
        <PrimaryNav
          route={route}
          navigate={navigate}
          waitingCount={waitingCount}
          principal={principal}
        />
        <MobileNav
          route={route}
          navigate={navigate}
          waitingCount={waitingCount}
          principal={principal}
        />
      </div>

      {isEditor ? (
        <WorkflowIdentity
          workflow={workflow}
          selectOptions={selectOptions}
          switchWorkflow={switchWorkflow}
          dirty={dirty}
          autosaving={autosaving}
          busyNew={busyNew}
          createBlank={createBlank}
        />
      ) : (
        <div className="topbar-spacer" />
      )}

      {hasRightZone && (
        <div className="topbar-zone topbar-actions">
          {isEditor && (
            <EditorActions
              errorCount={errorCount}
              warningCount={warningCount}
              issues={issues}
              setSelectedId={setSelectedId}
              undo={undo}
              redo={redo}
              undoStack={undoStack}
              redoStack={redoStack}
              save={save}
              saving={saving}
              online={online}
            />
          )}
          {isEditor && (
            <PublishControls
              startRun={startRun}
              starting={starting}
              runBlocked={runBlocked}
              publish={publish}
              publishing={publishing}
              publishPhase={publishPhase}
              saving={saving}
              online={online}
              more={
                <MoreMenu
                  workflow={workflow}
                  publishedVersion={publishedVersion}
                  hasDraft={hasDraft}
                  setVersionsOpen={setVersionsOpen}
                  onEditEgress={onEditEgress}
                  upgradeCount={upgradeCount}
                  onUpgradePreview={onUpgradePreview}
                />
              }
            />
          )}
          {principal?.id === 'anonymous' && (
            <span
              className="anon-chip"
              title="This server is running with --allow-anonymous; every unauthenticated request gets full admin access."
            >
              Anonymous admin (development mode)
            </span>
          )}
        </div>
      )}

      <SettingsMenu
        principal={principal}
        navigate={navigate}
        theme={theme}
        setTheme={setTheme}
        setAccountOpen={setAccountOpen}
        signOut={signOut}
        setPaletteOpen={setPaletteOpen}
      />
    </header>
  );
}

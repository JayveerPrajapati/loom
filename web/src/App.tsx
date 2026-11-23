import { useCallback, useRef, useState, useEffect, useMemo } from 'react';
import { AdminPanel } from './components/AdminPanel';
import { LoginPage } from './components/LoginPage';
import { TopBar } from './components/TopBar';
import { PrincipalRoleProvider } from './principal';
import { useRunStream } from './useRunStream';
import { useDialogFocus } from './useDialogFocus';
import { useMenuDetailsClose } from './menuBehavior';
import { useRouteSync } from './hooks/useRouteSync';
import { useAppKeyboard } from './hooks/useAppKeyboard';
import { useGraphEdits } from './hooks/useGraphEdits';
import { useWorkflowLifecycle } from './hooks/useWorkflowLifecycle';
import { useWorkflowListActions } from './hooks/useWorkflowListActions';
import { useWorkflowRun } from './hooks/useWorkflowRun';
import { useSession } from './hooks/useSession';
import { useWorkflowEditor } from './hooks/useWorkflowEditor';
import { useWorkspaceData } from './hooks/useWorkspaceData';
import { api } from './api';
import { assetKeys, invalidateAssets } from './assetCache';
import { useToast, useTheme, useWaitingCount, useRunInputDialog } from './hooks/useAppChrome';
import { CommandPalette, type Command } from './components/CommandPalette';
import { AppRoutes } from './components/AppRoutes';
import {
  BootErrorScreen,
  BootScreen,
  EmptyEditor,
  OfflineBanner,
  PublishWarningsBanner,
  RunHistoryHost,
  UpgradePreviewDialog,
} from './components/appChrome';
import { WorkflowEgressDialog } from './components/WorkflowEgressDialog';
import { EditorWorkspace } from './components/EditorWorkspace';
import { VersionHistory } from './components/VersionHistory';
import type { AuthResponse, ValidationIssue, Workflow } from './types';
import { buildCommands } from './appCommands';
import { formatCount, parseRoute, workflowSelectOptions, type Route } from './appShared';
import { validateWorkflow } from './validate';
import { deriveNodeStatuses, deriveRunStatus } from './runState';
import { ConfirmHost } from './confirm';
import { confirmAction } from './confirm';

export default function App() {
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [accountOpen, setAccountOpen] = useState(false);
  const [runId, setRunId] = useState<string | null>(null);
  const [upgradeOpen, setUpgradeOpen] = useState(false);
  const [egressOpen, setEgressOpen] = useState(false);
  const [route, setRoute] = useState<Route>(() => parseRoute());

  // Details-based dropdowns (settings, kebabs) close on outside click
  // and Escape — one listener for every menu in the app.
  useMenuDetailsClose();

  // Session-loss and sign-in close every dialog: flags live at App level
  // and would otherwise remount stale over the topbar (see useSession).
  // The run-input dialog's state lives in useAppChrome, so it is closed
  // through a registered callback.
  const closeRunInputRef = useRef<() => void>(() => {});
  const closeDialogs = useCallback(() => {
    setAccountOpen(false);
    setPaletteOpen(false);
    setVersionsOpen(false);
    closeRunInputRef.current();
  }, []);
  const session = useSession(closeDialogs);
  const {
    lifecycleHandlers,
    phase,
    authRequired,
    authError,
    authExpired,
    anonymousLogin,
    bootError,
    principal,
    online,
    subprocess,
    checkConnection,
    signOut,
    onAuthenticated,
  } = session;
  const workspace = useWorkspaceData();
  const {
    dataHandlers,
    defs,
    examples,
    summaries,
    defsByType,
    profilesById,
    schemasById,
    mappingsById,
    refreshProfiles,
    refreshSchemas,
    refreshMappings,
    refreshSummaries,
  } = workspace;

  // Campaign 4: connector mutations change the node-types listing (the
  // palette), so the editor refreshes defs when connectors change.
  const refreshDefs = useCallback(() => {
    api
      .nodeTypes()
      .then((result) => dataHandlers.setDefs(result.nodeTypes ?? []))
      .catch(() => undefined);
  }, [dataHandlers]);

  const {
    workflow,
    workflowRef,
    dirty,
    selectedId,
    setSelectedId,
    undoStack,
    redoStack,
    commit,
    apply,
    undo,
    redo,
    acceptSaved,
    adopt,
    clear,
  } = useWorkflowEditor();

  const { theme, setTheme } = useTheme();
  const { notify, node: toastNode } = useToast(route);
  const { waitingCount, refreshWaiting } = useWaitingCount(principal);

  // Run + event state with the live SSE stream attached (see useRunStream).
  const { run, setRun, events, setEvents } = useRunStream(runId, notify);

  const discardEditor = useCallback(() => {
    clear();
    setSelectedId(null);
    setRun(null);
    setEvents([]);
    setRunId(null);
    setVersionsOpen(false);
  }, []);

  const { navigate } = useRouteSync(route, setRoute, discardEditor, dirty);

  const adoptWorkflow = useCallback(
    (next: Workflow, markDirty = false) => {
      adopt(next, markDirty);
      setRun(null);
      setEvents([]);
      setRunId(null);
      setVersionsOpen(false);
    },
    [adopt],
  );

  const {
    versionState,
    upgradePlan,
    upgradeIssues,
    refreshWorkflowState,
    loadInitial,
  } = useWorkflowLifecycle({
    route,
    workflowRef,
    adoptWorkflow,
    clearEditor: clear,
    notify,
    session: lifecycleHandlers,
    data: dataHandlers,
  });

  const upgradeDialogRef = useDialogFocus<HTMLDivElement>(
    Boolean(upgradeOpen && upgradePlan && upgradePlan.length > 0),
    () => setUpgradeOpen(false),
  );

  const onAuthenticatedWrapper = useCallback(
    async (response: AuthResponse) => {
      await onAuthenticated(response, loadInitial);
    },
    [loadInitial, onAuthenticated],
  );

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (route.name === 'home' || route.name === 'workflows') void refreshSummaries();
  }, [refreshSummaries, route.name]);

  const {
    saving,
    publishing,
    publishPhase,
    starting,
    autosaving,
    publish,
    save,
    applyUpgrade,
    startRun,
    retryFailedNode,
    cancelRun,
    resumeRun,
    openRunInput: openRunInputWith,
    submitRunInput: submitRunInputWith,
    publishWarnings,
    clearPublishWarnings,
  } = useWorkflowRun({
    workflowRef,
    workflow,
    dirty,
    online,
    subprocess,
    defsByType,
    profilesById,
    schemasById,
    mappingsById,
    notify,
    refreshSummaries,
    refreshWorkflowState,
    acceptSaved,
    setSelectedId,
    run,
    setRun,
    events,
    setEvents,
    runId,
    setRunId,
  });

  const registerRunInputClose = useCallback((close: () => void) => {
    closeRunInputRef.current = close;
  }, []);
  const { openRunInput, close: closeRunInput, node: runInputDialog } = useRunInputDialog({
    openRunInputWith,
    submitRunInputWith,
    registerClose: registerRunInputClose,
  });

  const { addNode, moveNode, deleteNodes, deleteEdges, connect } = useGraphEdits({
    workflowRef,
    defsByType,
    commit,
    apply,
    notify,
    setSelectedId,
  });

  const deleteSelected = useCallback(() => {
    if (!selectedId) return;
    const nodeName = workflow?.nodes.find((node) => node.id === selectedId)?.name;
    void confirmAction({
      title: nodeName ? `Delete “${nodeName}”?` : 'Delete this node?',
      body: 'The node and its edges are removed from the draft. Undo restores them.',
      confirmLabel: 'Delete node',
      cancelLabel: 'Cancel',
    }).then((confirmed) => {
      if (confirmed) deleteNodes([selectedId]);
    });
  }, [deleteNodes, selectedId, workflow?.nodes]);

  const {
    busyNew,
    createBlank,
    createFromExample,
    switchWorkflow,
    renameTemplate,
    deleteTemplate,
    openRunById,
    runFromView,
    openRunbook,
  } = useWorkflowListActions({
    workflowRef,
    defsByType,
    dirty,
    summaries,
    adoptWorkflow,
    navigate,
    notify,
    refreshSummaries,
    startRun,
  });

  const issues = useMemo<ValidationIssue[]>(
    () => (workflow ? validateWorkflow(workflow, defsByType, profilesById, schemasById, mappingsById) : []),
    [workflow, defsByType, profilesById, schemasById, mappingsById],
  );
  const errorCount = issues.filter((issue) => issue.severity === 'error').length;
  const warningCount = issues.length - errorCount;
  const selectedNode = workflow?.nodes.find((node) => node.id === selectedId) ?? null;
  const status = deriveRunStatus(events, run?.status ?? '');
  const statuses = useMemo(
    () => deriveNodeStatuses(events, workflow?.nodes.map((node) => node.id), status),
    [events, status, workflow],
  );
  const runBlocked = !online
    ? 'Loom server is unreachable'
    : errorCount
      ? 'Fix validation errors before running'
      : undefined;

  const commands = useMemo<Command[]>(
    () =>
      buildCommands({
        routeName: route.name,
        workflow,
        theme,
        examples,
        defs,
        startRun: () => void startRun(),
        save: () => void save(),
        createBlank: () => void createBlank(),
        apply,
        notify,
        navigate,
        setTheme,
        setAccountOpen,
        signOut: () => void signOut(),
        createFromExample: (example) => void createFromExample(example),
        addNode,
        formatCount,
      }),
    [
      addNode, apply, createBlank, createFromExample, examples, formatCount,
      navigate, notify, route.name, save, setAccountOpen, setTheme, signOut,
      startRun, theme, workflow,
    ],
  );

  useAppKeyboard({
    routeName: route.name,
    paletteOpen,
    setPaletteOpen,
    versionsOpen,
    accountOpen,
    setVersionsOpen,
    setAccountOpen,
    closeRunInput,
    save,
    startRun: () => void startRun(),
    undo,
    redo,
  });

  if (!session.authChecked || phase === 'loading') {
    return <BootScreen />;
  }

  if (authRequired || !principal) {
    return (
      <LoginPage
        onSuccess={onAuthenticatedWrapper}
        message={authError}
        messageTone={authExpired ? 'notice' : 'error'}
        anonymous={anonymousLogin}
      />
    );
  }

  if (phase === 'error') {
    return <BootErrorScreen bootError={bootError} onRetry={() => void loadInitial()} />;
  }

  const publishedVersion = versionState?.publishedVersion ?? 0;
  const hasDraft = Boolean(workflow) && (dirty || versionState?.draft || publishedVersion === 0);
  const selectOptions = workflowSelectOptions(workflow, summaries, publishedVersion, hasDraft);

  return (
    <div className="app">
      <a className="skip-link" href="#main-stage">Skip to canvas</a>
      {!online && <OfflineBanner onRetry={() => void checkConnection()} />}
      <TopBar
        route={route}
        navigate={navigate}
        waitingCount={waitingCount}
        workflow={workflow}
        selectOptions={selectOptions}
        switchWorkflow={switchWorkflow}
        dirty={dirty}
        autosaving={autosaving}
        busyNew={busyNew}
        createBlank={createBlank}
        publishedVersion={publishedVersion}
        hasDraft={hasDraft}
        publishing={publishing}
        publishPhase={publishPhase}
        saving={saving}
        online={online}
        setVersionsOpen={setVersionsOpen}
        publish={publish}
        upgradeCount={upgradePlan?.length ?? 0}
        onUpgradePreview={() => setUpgradeOpen(true)}
        onEditEgress={() => setEgressOpen(true)}
        errorCount={errorCount}
        warningCount={warningCount}
        issues={issues}
        setSelectedId={setSelectedId}
        undo={undo}
        redo={redo}
        undoStack={undoStack}
        redoStack={redoStack}
        principal={principal}
        theme={theme}
        setTheme={setTheme}
        setAccountOpen={setAccountOpen}
        signOut={signOut}
        setPaletteOpen={setPaletteOpen}
        save={save}
        startRun={startRun}
        starting={starting}
        runBlocked={runBlocked}
      />

      <PrincipalRoleProvider role={principal?.role}>
        <AppRoutes
          route={route}
        workflow={workflow}
        summaries={summaries}
        examples={examples}
        defsByType={defsByType}
        issues={issues}
        statuses={statuses}
        selectedId={selectedId}
        online={online}
        principalRole={principal?.role}
        onNavigate={navigate}
        onSelect={setSelectedId}
        onNew={() => void createBlank()}
        onOpenTemplate={(id) => void switchWorkflow(id)}
        onUseExample={(example) => void createFromExample(example)}
        onRenameTemplate={renameTemplate}
        onDeleteTemplate={deleteTemplate}
        onRunFromView={() => void runFromView()}
        onOpenRun={(runID) => void openRunById(runID)}
        onOpenRunFromRunsPage={(runID) => void openRunById(runID)}
        onNotify={notify}
        onWaitingChanged={refreshWaiting}
        onProfilesChanged={refreshProfiles}
        onSchemasChanged={refreshSchemas}
        onMappingsChanged={refreshMappings}
        onConnectorsChanged={refreshDefs}
        onScriptsChanged={() => invalidateAssets(assetKeys.scripts)}
          subprocess={subprocess}
        />
      </PrincipalRoleProvider>

      {route.name === 'editor' && workflow ? (
        <EditorWorkspace
          workflow={workflow}
          defs={defs}
          defsByType={defsByType}
          statuses={statuses}
          issues={issues}
          selectedId={selectedId}
          selectedNode={selectedNode}
          run={run}
          status={status}
          events={events}
          busy={starting}
          online={online}
          runBlocked={runBlocked}
          subprocessEnabled={subprocess?.enabled}
          onSelect={setSelectedId}
          onAddNode={addNode}
          onMoveNode={moveNode}
          onDeleteNodes={deleteNodes}
          onDeleteEdges={deleteEdges}
          onConnect={connect}
          onRun={() => void startRun()}
          onInput={openRunInput}
          onRetry={() => void retryFailedNode()}
          onCancel={() => void cancelRun()}
          onResume={(decision) => void resumeRun(decision)}
          onNotify={notify}
          onOpenHistory={() => setHistoryOpen(true)}
          onOpenRun={(runID) => void openRunById(runID)}
          onOpenRunbook={() => void openRunbook()}
          onOpenWorkflows={() => navigate('/workflows')}
          onOpenHub={(nodeType) => navigate(`/hub/${encodeURIComponent(nodeType)}`)}
          onMappingsChanged={refreshMappings}
          onApply={apply}
          onDeleteSelected={deleteSelected}
        />
      ) : route.name === 'editor' ? (
        <EmptyEditor onBrowse={() => navigate('/workflows')} />
      ) : null}

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />
      <ConfirmHost />

      {workflow && (
        <VersionHistory
          open={versionsOpen}
          workflowId={workflow.id}
          current={workflow}
          state={versionState}
          onClose={() => setVersionsOpen(false)}
          onPublish={() => void publish()}
        />
      )}

      {workflow && egressOpen && (
        <WorkflowEgressDialog
          workflow={workflow}
          notify={notify}
          apply={apply}
          onClose={() => setEgressOpen(false)}
        />
      )}

      {workflow && upgradeOpen && upgradePlan && upgradePlan.length > 0 && (
        <UpgradePreviewDialog
          plan={upgradePlan}
          issues={upgradeIssues}
          applying={starting}
          focusRef={upgradeDialogRef}
          onClose={() => setUpgradeOpen(false)}
          onApply={() => void applyUpgrade(() => setUpgradeOpen(false))}
        />
      )}

      <PublishWarningsBanner warnings={publishWarnings} onDismiss={clearPublishWarnings} />

      <RunHistoryHost
        open={historyOpen}
        workflowId={workflow?.id ?? null}
        currentRunId={run?.id ?? null}
        onClose={() => setHistoryOpen(false)}
        setRun={setRun}
        setEvents={setEvents}
        setRunId={setRunId}
        onOpenRunById={(historicalRunId) => void openRunById(historicalRunId)}
      />

      {runInputDialog}

      {accountOpen && principal && (
        <AdminPanel
          principal={principal}
          onClose={() => setAccountOpen(false)}
          onSignOut={() => {
            setAccountOpen(false);
            void signOut();
          }}
          onNotify={notify}
        />
      )}

      {toastNode}
    </div>
  );
}

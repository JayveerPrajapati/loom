import {
  CreateJobPage,
  HomePage,
  JobDetailPage,
  JobsPage,
  RunsPage,
  TasksPage,
  TemplatesPage,
} from './DashboardPages';
import { CredentialsPage } from './CredentialsPage';
import { OpsPage } from './OpsPage';
import { CapabilitiesPage } from './CapabilitiesPage';
import { AuditPage } from './AuditPage';
import { ProfilesLandingPage } from './ProfilesLandingPage';
import { ProfileFormPage } from './ProfileFormPage';
import { ProfileViewPage } from './ProfileViewPage';
import { SchemasPage } from './SchemasPage';
import { MappingsPage } from './MappingsPage';
import { ScriptsPage } from './ScriptsPage';
import { ConnectorsPage } from './ConnectorsPage';
import { HubNodeTypePage } from './HubNodeTypePage';
import { RunDetailPage } from './RunDetailPage';
import { TemplateViewPage } from './TemplateViewPage';
import type { NodeStatus } from './Canvas';
import type { Route } from '../appShared';
import type {
  Example,
  NodeDef,
  SubprocessNodes,
  ValidationIssue,
  Workflow,
  WorkflowSummary,
} from '../types';

interface Props {
  route: Route;
  workflow: Workflow | null;
  summaries: WorkflowSummary[];
  examples: Example[];
  defsByType: Record<string, NodeDef>;
  issues: ValidationIssue[];
  statuses: Record<string, NodeStatus>;
  selectedId: string | null;
  online: boolean;
  principalRole: string | undefined;
  onNavigate: (path: string) => void;
  onSelect: (id: string | null) => void;
  onNew: () => void;
  onOpenTemplate: (id: string) => void;
  onUseExample: (example: Example) => void;
  onRenameTemplate: (id: string, name: string) => Promise<void>;
  onDeleteTemplate: (id: string) => Promise<void>;
  onRunFromView: () => void;
  onOpenRun: (runID: string) => void;
  onOpenRunFromRunsPage: (runID: string) => void;
  onNotify: (message: string, tone?: 'info' | 'error') => void;
  onWaitingChanged: () => void;
  onProfilesChanged: () => void;
  onSchemasChanged: () => void;
  onMappingsChanged: () => void;
  onScriptsChanged?: () => void;
  onConnectorsChanged?: () => void;
  subprocess?: SubprocessNodes | null;
}

// Non-editor routes, extracted verbatim from App: this component owns no
// state, it maps the current route to a page with pass-through callbacks.
export function AppRoutes({
  route,
  workflow,
  summaries,
  examples,
  defsByType,
  issues,
  statuses,
  selectedId,
  online,
  principalRole,
  onNavigate,
  onSelect,
  onNew,
  onOpenTemplate,
  onUseExample,
  onRenameTemplate,
  onDeleteTemplate,
  onRunFromView,
  onOpenRun,
  onOpenRunFromRunsPage,
  onNotify,
  onWaitingChanged,
  onProfilesChanged,
  onSchemasChanged,
  onMappingsChanged,
  onScriptsChanged,
  onConnectorsChanged,
  subprocess,
}: Props) {
  return (
    <>
      {route.name === 'home' && (
        <HomePage
          summaries={summaries}
          examples={examples}
          onNew={onNew}
          onOpenTemplate={onOpenTemplate}
          onUseExample={onUseExample}
          onOpenWorkflows={() => onNavigate('/workflows')}
          onOpenRuns={() => onNavigate('/runs')}
          onOpenRun={(runID) => onOpenRunFromRunsPage(runID)}
        />
      )}

      {route.name === 'workflows' && (
        <TemplatesPage
          examples={examples}
          onNew={onNew}
          onUseExample={onUseExample}
          onOpenTemplate={onOpenTemplate}
          onRenameTemplate={onRenameTemplate}
          onDeleteTemplate={onDeleteTemplate}
        />
      )}

      {route.name === 'workflow' && workflow && workflow.id === route.id && (
        <TemplateViewPage
          workflow={workflow}
          defs={defsByType}
          issues={issues}
          statuses={statuses}
          selectedId={selectedId}
          online={online}
          onSelect={onSelect}
          onBack={() => onNavigate('/workflows')}
          onEdit={() => onNavigate(`/workflows/${encodeURIComponent(workflow.id)}/edit`)}
          onRun={onRunFromView}
          onOpenRun={(runID) => onOpenRun(runID)}
        />
      )}

      {route.name === 'workflow' && !workflow && (
        <main className="page-shell" id="main-stage" tabIndex={-1}>
          <div className="empty-panel">
            <h2>Template not found</h2>
            <p className="muted">The template could not be loaded.</p>
            <button className="button primary" onClick={() => onNavigate('/workflows')}>
              Browse workflows
            </button>
          </div>
        </main>
      )}

      {route.name === 'jobs' && (
        <JobsPage
          summaries={summaries}
          onOpenJob={(id) => onNavigate(`/jobs/${encodeURIComponent(id)}`)}
          onCreateJob={() => onNavigate('/jobs/new')}
          onOpenWorkflows={() => onNavigate('/workflows')}
          onOpenRun={(runID) => onOpenRunFromRunsPage(runID)}
          onNotify={onNotify}
        />
      )}

      {route.name === 'job-new' && (
        <CreateJobPage
          summaries={summaries}
          onCreated={(id) => onNavigate(`/jobs/${encodeURIComponent(id)}`)}
          onCancel={() => onNavigate('/jobs')}
          onOpenWorkflows={() => onNavigate('/workflows')}
        />
      )}

      {(route.name === 'job' || route.name === 'job-edit') && (
        <JobDetailPage
          jobId={route.id ?? ''}
          summaries={summaries}
          editing={route.name === 'job-edit'}
          onBack={() => onNavigate('/jobs')}
          onEdit={() => onNavigate(`/jobs/${encodeURIComponent(route.id ?? '')}/edit`)}
          onDone={() => onNavigate(`/jobs/${encodeURIComponent(route.id ?? '')}`)}
          onOpenTemplate={onOpenTemplate}
          onOpenRun={(runID) => onOpenRunFromRunsPage(runID)}
          onOpenJob={(id) => onNavigate(`/jobs/${encodeURIComponent(id)}`)}
          onNotify={onNotify}
        />
      )}

      {route.name === 'runs' && (
        <RunsPage summaries={summaries} onOpenRun={(runID) => onOpenRunFromRunsPage(runID)} />
      )}

      {route.name === 'tasks' && (
        <TasksPage
          summaries={summaries}
          canApprove={principalRole !== 'viewer'}
          onOpenRun={(runID) => onOpenRunFromRunsPage(runID)}
          onNotify={onNotify}
          onWaitingChanged={onWaitingChanged}
        />
      )}

      {route.name === 'run' && route.id && (
        <RunDetailPage
          key={route.id}
          runId={route.id}
          summaries={summaries}
          defs={defsByType}
          onBack={() => onNavigate('/runs')}
          onOpenRun={(runID) => onOpenRun(runID)}
          onOpenTemplate={onOpenTemplate}
          onOpenJob={(id) => onNavigate(`/jobs/${encodeURIComponent(id)}`)}
          onWaitingChanged={onWaitingChanged}
          onNotify={onNotify}
        />
      )}

      {route.name === 'credentials' && <CredentialsPage onNotify={onNotify} />}

      {route.name === 'ops' && <OpsPage onNavigate={onNavigate} />}

      {route.name === 'capabilities' && <CapabilitiesPage principalRole={principalRole} />}

      {route.name === 'audit' && <AuditPage principalRole={principalRole} />}

      {route.name === 'profiles' && (
        <ProfilesLandingPage defs={Object.values(defsByType)} onNavigate={onNavigate} />
      )}

      {(route.name === 'profile-new' || route.name === 'profile-edit') && (
        <ProfileFormPage
          key={route.name === 'profile-edit' ? route.profileId : route.nodeType}
          nodeType={route.nodeType ?? ''}
          profileId={route.name === 'profile-edit' ? route.profileId : undefined}
          def={route.nodeType ? defsByType[route.nodeType] : undefined}
          onNavigate={onNavigate}
          onNotify={onNotify}
          onProfilesChanged={onProfilesChanged}
        />
      )}

      {route.name === 'profile-view' && (
        <ProfileViewPage
          key={route.profileId}
          nodeType={route.nodeType ?? ''}
          profileId={route.profileId ?? ''}
          def={route.nodeType ? defsByType[route.nodeType] : undefined}
          principalRole={principalRole}
          onNavigate={onNavigate}
        />
      )}

      {route.name === 'schemas' && <SchemasPage onNotify={onNotify} onSchemasChanged={onSchemasChanged} />}

      {route.name === 'mappings' && (
        <MappingsPage onNotify={onNotify} onMappingsChanged={onMappingsChanged} />
      )}

      {route.name === 'scripts' && (
        <ScriptsPage
          onNotify={onNotify}
          onScriptsChanged={onScriptsChanged}
          subprocess={subprocess}
        />
      )}

      {route.name === 'connectors' && (
        <ConnectorsPage onNotify={onNotify} onConnectorsChanged={onConnectorsChanged} />
      )}

      {route.name === 'hub-node' && (
        <HubNodeTypePage
          key={route.nodeType}
          nodeType={route.nodeType ?? ''}
          def={route.nodeType ? defsByType[route.nodeType] : undefined}
          onNavigate={onNavigate}
          onNotify={onNotify}
          onProfilesChanged={onProfilesChanged}
        />
      )}

      {route.name === 'not-found' && (
        <main className="page-shell" id="main-stage" tabIndex={-1}>
          <div className="page-head">
            <div>
              <h1>Page not found</h1>
              <p className="lede">
                Nothing lives at <code>{route.path}</code>.
              </p>
            </div>
            <button className="button primary" onClick={() => onNavigate('/')}>
              Back to home
            </button>
          </div>
        </main>
      )}
    </>
  );
}

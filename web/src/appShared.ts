import type { Workflow, WorkflowSummary } from './types';

export type Phase = 'loading' | 'ready' | 'error';
export type Toast = { message: string; tone: 'info' | 'error' };

export type Route = {
  name:
    | 'home'
    | 'workflows'
    | 'workflow'
    | 'jobs'
    | 'job-new'
    | 'job'
    | 'job-edit'
    | 'runs'
    | 'run'
    | 'tasks'
    | 'credentials'
    | 'ops'
    | 'capabilities'
    | 'audit'
    | 'profiles'
    | 'profile-new'
    | 'profile-edit'
    | 'profile-view'
    | 'schemas'
    | 'mappings'
    | 'scripts'
    | 'connectors'
    | 'hub-node'
    | 'editor'
    | 'not-found';
  id?: string;
  path?: string;
  /** Node type for the node-type family routes (/hub/:nodeType…). */
  nodeType?: string;
  /** Profile id for the profile edit route. */
  profileId?: string;
  /** True when the URL used a legacy path family (/templates, or the
   * pre-merge /profiles/:nodeType pages that now live under /hub); the
   * App redirects it with history.replaceState. */
  legacy?: boolean;
};

export function parseRoute(pathname = window.location.pathname): Route {
  if (pathname === '/' || pathname === '') return { name: 'home' };
  if (pathname === '/workflows' || pathname === '/templates') {
    const route: Route = { name: 'workflows' };
    if (pathname === '/templates') route.legacy = true;
    return route;
  }
  if (pathname === '/jobs') return { name: 'jobs' };
  if (pathname === '/jobs/new') return { name: 'job-new' };
  if (pathname === '/credentials') return { name: 'credentials' };
  if (pathname === '/ops') return { name: 'ops' };
  if (pathname === '/capabilities') return { name: 'capabilities' };
  if (pathname === '/audit') return { name: 'audit' };
  if (pathname === '/profiles') return { name: 'profiles' };
  // v4.1 E1: the /profiles/:nodeType family merged into /hub/:nodeType.
  // Legacy URLs keep parsing so links never dead-end; the App rewrites
  // the address bar to the /hub equivalent.
  const profileNew = pathname.match(/^\/profiles\/([^/]+)\/new\/?$/);
  if (profileNew) {
    return { name: 'profile-new', nodeType: decodeURIComponent(profileNew[1]), legacy: true };
  }
  const profileEdit = pathname.match(/^\/profiles\/([^/]+)\/([^/]+)\/edit\/?$/);
  if (profileEdit) {
    return {
      name: 'profile-edit',
      nodeType: decodeURIComponent(profileEdit[1]),
      profileId: decodeURIComponent(profileEdit[2]),
      legacy: true,
    };
  }
  const profilesType = pathname.match(/^\/profiles\/([^/]+)\/?$/);
  if (profilesType) {
    return { name: 'hub-node', nodeType: decodeURIComponent(profilesType[1]), legacy: true };
  }
  if (pathname === '/schemas') return { name: 'schemas' };
  if (pathname === '/mappings') return { name: 'mappings' };
  if (pathname === '/scripts') return { name: 'scripts' };
  if (pathname === '/connectors') return { name: 'connectors' };
  // Profile-Centric UX Phase 2 (D2-1): the hub landing merged into the
  // profiles landing; /hub keeps parsing so old links never dead-end and
  // the App rewrites the address bar to /profiles.
  if (pathname === '/hub' || pathname === '/hub/') return { name: 'profiles', legacy: true };
  const hubProfileNew = pathname.match(/^\/hub\/([^/]+)\/new\/?$/);
  if (hubProfileNew) return { name: 'profile-new', nodeType: decodeURIComponent(hubProfileNew[1]) };
  const hubProfileEdit = pathname.match(/^\/hub\/([^/]+)\/([^/]+)\/edit\/?$/);
  if (hubProfileEdit) {
    return {
      name: 'profile-edit',
      nodeType: decodeURIComponent(hubProfileEdit[1]),
      profileId: decodeURIComponent(hubProfileEdit[2]),
    };
  }
  // UX Phase 2 2.7b: read-only profile view (no /edit suffix).
  const hubProfileView = pathname.match(/^\/hub\/([^/]+)\/([^/]+)\/?$/);
  if (hubProfileView) {
    return {
      name: 'profile-view',
      nodeType: decodeURIComponent(hubProfileView[1]),
      profileId: decodeURIComponent(hubProfileView[2]),
    };
  }
  const hubNode = pathname.match(/^\/hub\/([^/]+)\/?$/);
  if (hubNode) return { name: 'hub-node', nodeType: decodeURIComponent(hubNode[1]) };
  const jobEdit = pathname.match(/^\/jobs\/([^/]+)\/edit\/?$/);
  if (jobEdit) return { name: 'job-edit', id: decodeURIComponent(jobEdit[1]) };
  const job = pathname.match(/^\/jobs\/([^/]+)\/?$/);
  if (job) return { name: 'job', id: decodeURIComponent(job[1]) };
  if (pathname === '/runs') return { name: 'runs' };
  if (pathname === '/tasks') return { name: 'tasks' };
  const run = pathname.match(/^\/runs\/([^/]+)\/?$/);
  if (run) return { name: 'run', id: decodeURIComponent(run[1]) };
  const editor = pathname.match(/^\/(?:workflows|templates)\/([^/]+)\/edit\/?$/);
  if (editor) {
    const route: Route = { name: 'editor', id: decodeURIComponent(editor[1]) };
    if (editor[0].startsWith('/templates')) route.legacy = true;
    return route;
  }
  const workflow = pathname.match(/^\/(?:workflows|templates)\/([^/]+)\/?$/);
  if (workflow) {
    const route: Route = { name: 'workflow', id: decodeURIComponent(workflow[1]) };
    if (workflow[0].startsWith('/templates')) route.legacy = true;
    return route;
  }
  return { name: 'not-found', path: pathname };
}

export function cloneWorkflow(workflow: Workflow): Workflow {
  return JSON.parse(JSON.stringify(workflow)) as Workflow;
}

export function normalizeWorkflow(workflow: Workflow): Workflow {
  return {
    ...workflow,
    nodes: workflow.nodes ?? [],
    edges: workflow.edges ?? [],
    settings: workflow.settings ?? {},
  };
}

/** The topbar's workflow selector list: summaries, with the open
 *  workflow prepended when it is not among them yet (fresh create). */
export function workflowSelectOptions(
  workflow: Workflow | null,
  summaries: WorkflowSummary[],
  publishedVersion: number,
  hasDraft: boolean,
): WorkflowSummary[] {
  if (!workflow) return summaries;
  if (summaries.some((summary) => summary.id === workflow.id)) return summaries;
  return [
    {
      id: workflow.id,
      name: workflow.name,
      version: workflow.version,
      publishedVersion,
      draft: hasDraft,
      updatedAt: '',
    },
    ...summaries,
  ];
}

export function formatCount(count: number, singular: string): string {
  return `${count} ${singular}${count === 1 ? '' : 's'}`;
}

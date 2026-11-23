import { describe, expect, it } from 'vitest';
import { formatCount, normalizeWorkflow, parseRoute } from '../appShared';
import type { Workflow } from '../types';

describe('parseRoute', () => {
  it('maps every static path', () => {
    expect(parseRoute('/')).toEqual({ name: 'home' });
    expect(parseRoute('')).toEqual({ name: 'home' });
    expect(parseRoute('/workflows')).toEqual({ name: 'workflows' });
    expect(parseRoute('/jobs')).toEqual({ name: 'jobs' });
    expect(parseRoute('/jobs/new')).toEqual({ name: 'job-new' });
    expect(parseRoute('/credentials')).toEqual({ name: 'credentials' });
    expect(parseRoute('/runs')).toEqual({ name: 'runs' });
    expect(parseRoute('/tasks')).toEqual({ name: 'tasks' });
  });

  it('extracts and decodes ids, accepting an optional trailing slash', () => {
    expect(parseRoute('/jobs/j-1')).toEqual({ name: 'job', id: 'j-1' });
    expect(parseRoute('/jobs/j-1/')).toEqual({ name: 'job', id: 'j-1' });
    expect(parseRoute('/jobs/j%201')).toEqual({ name: 'job', id: 'j 1' });
    expect(parseRoute('/runs/r-1')).toEqual({ name: 'run', id: 'r-1' });
    expect(parseRoute('/workflows/w-1')).toEqual({ name: 'workflow', id: 'w-1' });
    expect(parseRoute('/jobs/j-1/edit')).toEqual({ name: 'job-edit', id: 'j-1' });
    expect(parseRoute('/workflows/w-1/edit')).toEqual({ name: 'editor', id: 'w-1' });
  });

  it('keeps legacy /templates paths on the same routes, flagged for redirect', () => {
    expect(parseRoute('/templates')).toEqual({ name: 'workflows', legacy: true });
    expect(parseRoute('/templates/t-1')).toEqual({ name: 'workflow', id: 't-1', legacy: true });
    expect(parseRoute('/templates/t-1/edit')).toEqual({ name: 'editor', id: 't-1', legacy: true });
    expect(parseRoute('/workflows').legacy).toBeUndefined();
  });

  it('maps the node-type family routes under /hub', () => {
    expect(parseRoute('/profiles')).toEqual({ name: 'profiles' });
    // UX Phase 2 D2-1: the retired hub landing redirects to /profiles.
    expect(parseRoute('/hub')).toEqual({ name: 'profiles', legacy: true });
    expect(parseRoute('/hub/')).toEqual({ name: 'profiles', legacy: true });
    expect(parseRoute('/hub/http.request')).toEqual({
      name: 'hub-node',
      nodeType: 'http.request',
    });
    expect(parseRoute('/hub/http.request/')).toEqual({
      name: 'hub-node',
      nodeType: 'http.request',
    });
    expect(parseRoute('/hub/code.python/new')).toEqual({
      name: 'profile-new',
      nodeType: 'code.python',
    });
    expect(parseRoute('/hub/http.request/profile_abc/edit')).toEqual({
      name: 'profile-edit',
      nodeType: 'http.request',
      profileId: 'profile_abc',
    });
    // A node type is never mistaken for a profile id: "new" and "edit"
    // segments only match in their positions. The bare two-segment form
    // is the read-only profile view (UX Phase 2 2.7b).
    expect(parseRoute('/hub/http.request/profile_abc')).toEqual({
      name: 'profile-view',
      nodeType: 'http.request',
      profileId: 'profile_abc',
    });
  });

  it('keeps the pre-merge /profiles/:nodeType family on the same routes, flagged for redirect', () => {
    expect(parseRoute('/profiles/http.request')).toEqual({
      name: 'hub-node',
      nodeType: 'http.request',
      legacy: true,
    });
    expect(parseRoute('/profiles/code.python/new')).toEqual({
      name: 'profile-new',
      nodeType: 'code.python',
      legacy: true,
    });
    expect(parseRoute('/profiles/http.request/profile_abc/edit')).toEqual({
      name: 'profile-edit',
      nodeType: 'http.request',
      profileId: 'profile_abc',
      legacy: true,
    });
    expect(parseRoute('/profiles/http.request/profile_abc')).toEqual({
      name: 'not-found',
      path: '/profiles/http.request/profile_abc',
    });
    expect(parseRoute('/hub/http.request').legacy).toBeUndefined();
  });

  it('falls back to not-found with the original path', () => {
    expect(parseRoute('/nope/deeper')).toEqual({ name: 'not-found', path: '/nope/deeper' });
  });
});

describe('formatCount', () => {
  it('pluralizes only the singular case', () => {
    expect(formatCount(1, 'run')).toBe('1 run');
    expect(formatCount(3, 'run')).toBe('3 runs');
    expect(formatCount(0, 'node')).toBe('0 nodes');
  });
});

describe('normalizeWorkflow', () => {
  it('fills missing collections without touching the rest', () => {
    const raw = { id: 'w', name: 'w', version: 1 } as unknown as Workflow;
    const normalized = normalizeWorkflow(raw);
    expect(normalized.nodes).toEqual([]);
    expect(normalized.edges).toEqual([]);
    expect(normalized.settings).toEqual({});
    expect(normalized.id).toBe('w');
  });
});

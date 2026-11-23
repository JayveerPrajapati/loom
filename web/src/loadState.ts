import { ApiError } from './api';

export type LoadFailureKind = 'permission' | 'offline' | 'error';

export interface LoadFailure {
  kind: LoadFailureKind;
  title: string;
  message: string;
  retryable: boolean;
}

const PERMISSION_MESSAGE =
  'You do not have permission to view this. Ask an administrator for access.';
const OFFLINE_MESSAGE =
  'Loom cannot reach the server right now. Check your connection; nothing was changed.';

/**
 * One classification for every page's primary load: a denied request is a
 * permission state, a failed request while the browser is offline is an
 * offline state, and everything else is a retryable error. Pages render the
 * matching AssetState rather than collapsing all three into an empty list.
 */
export function classifyLoadFailure(error: unknown, online = true): LoadFailure {
  if (error instanceof ApiError) {
    if (error.status === 401 || error.status === 403) {
      return {
        kind: 'permission',
        title: 'Access needed',
        message: PERMISSION_MESSAGE,
        retryable: false,
      };
    }
    const retryable = error.status === 429 || error.status >= 500;
    return {
      kind: 'error',
      title: 'Could not load',
      message: error.message || 'The server could not complete this request.',
      retryable,
    };
  }
  if (!online) {
    return { kind: 'offline', title: 'Offline', message: OFFLINE_MESSAGE, retryable: true };
  }
  const detail = error instanceof Error && error.message ? error.message : '';
  return {
    kind: 'error',
    title: 'Could not load',
    message: detail || 'Something went wrong while loading this page.',
    retryable: true,
  };
}

export function isOfflineError(error: unknown, online: boolean): boolean {
  return classifyLoadFailure(error, online).kind === 'offline';
}

/** Page helper: reads the browser's current connectivity for the classifier. */
export function classifyCurrentLoadFailure(error: unknown): LoadFailure {
  const online = typeof navigator === 'undefined' ? true : navigator.onLine;
  return classifyLoadFailure(error, online);
}

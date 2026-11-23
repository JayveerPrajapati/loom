import { describe, expect, it } from 'vitest';
import { ApiError } from '../api';
import { classifyCurrentLoadFailure, classifyLoadFailure } from '../loadState';

describe('classifyLoadFailure', () => {
  it('maps a forbidden response to a non-retryable permission state', () => {
    const failure = classifyLoadFailure(new ApiError(403, 'forbidden'));
    expect(failure.kind).toBe('permission');
    expect(failure.retryable).toBe(false);
    expect(failure.message).toMatch(/permission/i);
  });

  it('maps an unauthorized response the same way', () => {
    expect(classifyLoadFailure(new ApiError(401, 'unauthorized')).kind).toBe('permission');
  });

  it('keeps a server error retryable with its own message', () => {
    const failure = classifyLoadFailure(new ApiError(500, 'store unavailable'));
    expect(failure).toMatchObject({ kind: 'error', retryable: true, message: 'store unavailable' });
  });

  it('does not offer retry for a client error that cannot succeed unchanged', () => {
    expect(classifyLoadFailure(new ApiError(400, 'bad filter')).retryable).toBe(false);
  });

  it('classifies a failed request while offline as offline, not error', () => {
    const failure = classifyLoadFailure(new TypeError('Failed to fetch'), false);
    expect(failure.kind).toBe('offline');
    expect(failure.message).toMatch(/cannot reach the server/i);
  });

  it('keeps an offline-marked API error as a normal error when the browser is online', () => {
    expect(classifyLoadFailure(new ApiError(503, 'starting'), true).kind).toBe('error');
  });

  it('falls back to a retryable generic error for unknown throwables', () => {
    const failure = classifyLoadFailure('boom');
    expect(failure).toMatchObject({ kind: 'error', retryable: true });
    expect(failure.message).toMatch(/went wrong/i);
  });

  it('classifies the current browser state without a second argument', () => {
    expect(classifyCurrentLoadFailure(new ApiError(403, 'forbidden')).kind).toBe('permission');
  });
});

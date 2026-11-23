import { describe, expect, it } from 'vitest';
import type { ConfigProfile, FieldDef, NodeDef, RetryPolicy } from '../types';
import {
  mandatoryError,
  paginationFromObject,
  paginationToObject,
  profileableCredentialSlots,
  profileableFieldGroups,
  profileChangeKeys,
  profilePayload,
  profileToValues,
  resolveProfileLocation,
  retryFromInputs,
  stripUnset,
  validateProfileConfig,
} from '../profileForm';

const string = (extra: Partial<FieldDef> = {}): FieldDef => ({ type: 'string', ...extra });

const httpDef: NodeDef = {
  type: 'http.request',
  typeVersion: 1,
  category: 'action',
  description: '',
  inputs: ['main'],
  outputs: ['main'],
  inputType: 'json',
  outputType: 'json',
  config: {
    method: string({ enum: ['GET', 'POST'], label: 'Method', profileable: true }),
    url: string({ label: 'URL', profileable: true }),
    timeout: { type: 'number', min: 1, max: 300, label: 'Timeout (seconds)', advanced: true, profileable: true },
    pagination: { type: 'object', label: 'Pagination', advanced: true, profileable: true },
    source: { type: 'string', label: 'Source' },
  },
  credentials: ['http'],
  optionalCredentials: ['http', 'oauth2'],
};

describe('profileableFieldGroups', () => {
  it('splits profileable fields into essential and advanced', () => {
    const { essential, advanced } = profileableFieldGroups(httpDef);
    expect(essential.map(([key]) => key)).toEqual(['method', 'url']);
    expect(advanced.map(([key]) => key)).toEqual(['timeout', 'pagination']);
  });
});

describe('stripUnset', () => {
  it('drops empty strings, undefined, empty objects and NaN', () => {
    expect(
      stripUnset({
        method: 'GET',
        url: '',
        contentType: undefined,
        pagination: {},
        timeout: Number('abc'),
        headers: { keep: 'x' },
      }),
    ).toEqual({ method: 'GET', headers: { keep: 'x' } });
  });
});

describe('mandatoryError', () => {
  it('requires at least one config field or a retry policy', () => {
    expect(mandatoryError({}, null)).toContain('at least one');
    expect(mandatoryError({ method: 'GET' }, null)).toBeNull();
    expect(mandatoryError({}, { maxAttempts: 3 })).toBeNull();
  });

  it('accepts a script-only bundle as included behavior', () => {
    expect(mandatoryError({}, null, { script: 'script-1' })).toBeNull();
  });
});

describe('validateProfileConfig', () => {
  it('rejects unknown and non-profileable keys', () => {
    const errors = validateProfileConfig(httpDef, { source: 'x', nope: 1 });
    expect(errors.some((error) => error.includes('not profileable'))).toBe(true);
    expect(errors.some((error) => error.includes('not a field'))).toBe(true);
  });

  it('checks enums, ranges and types', () => {
    const errors = validateProfileConfig(httpDef, {
      method: 'PATCH',
      timeout: 999,
      url: 42,
      pagination: [],
    });
    expect(errors.join('\n')).toContain('Method');
    expect(errors.join('\n')).toContain('Timeout');
    expect(errors.join('\n')).toContain('URL');
    expect(errors.join('\n')).toContain('Pagination');
  });

  it('accepts a valid partial profile', () => {
    expect(validateProfileConfig(httpDef, { method: 'GET', timeout: 30 })).toEqual([]);
  });
});

describe('retryFromInputs', () => {
  it('parses filled fields and omits empty ones', () => {
    const retry = retryFromInputs({ maxAttempts: '3', backoffMs: '', maxDelayMs: '4000' }) as RetryPolicy;
    expect(retry).toEqual({ maxAttempts: 3, maxDelayMs: 4000 });
  });

  it('returns null when nothing is set', () => {
    expect(retryFromInputs({ maxAttempts: '', backoffMs: '', maxDelayMs: '' })).toBeNull();
  });
});

describe('pagination round-trip', () => {
  it('round-trips the documented pagination keys', () => {
    const raw = {
      mode: 'cursor',
      cursorParam: 'after',
      cursorPath: 'next_cursor',
      limit: 100,
      maxItems: 5000,
    };
    const parsed = paginationFromObject(raw);
    expect(parsed.mode).toBe('cursor');
    expect(parsed.strings.cursorPath).toBe('next_cursor');
    expect(paginationToObject(parsed.mode, parsed.strings, parsed.numbers)).toEqual(raw);
  });

  it('returns null for an empty pagination editor', () => {
    expect(paginationToObject('', {}, {})).toBeNull();
  });
});

describe('profileableCredentialSlots', () => {
  it('merges and deduplicates required and optional slots', () => {
    expect(profileableCredentialSlots(httpDef)).toEqual(['http', 'oauth2']);
  });
});

describe('profilePayload', () => {
  it('assembles the API payload dropping empty optionals', () => {
    const payload = profilePayload({
      name: ' House API ',
      nodeType: 'http.request',
      description: '',
      owner: 'payments-team',
      tags: 'payments, , prod',
      config: { method: 'GET' },
      credentialRefs: { http: 'cred-1', oauth2: '' },
      retry: { maxAttempts: 3 },
    });
    expect(payload.name).toBe('House API');
    expect(payload.description).toBeUndefined();
    expect(payload.tags).toEqual(['payments', 'prod']);
    expect(payload.credentialRefs).toEqual({ http: 'cred-1' });
    expect(payload.retry).toEqual({ maxAttempts: 3 });
    expect(payload.owner).toBe('payments-team');
  });
});

describe('profileToValues', () => {
  it('seeds form values from the stored config', () => {
    const profile = { config: { method: 'POST', timeout: 30 } } as unknown as ConfigProfile;
    expect(profileToValues(profile)).toEqual({ method: 'POST', timeout: 30 });
  });
});

describe('profileChangeKeys', () => {
  it('lists set config keys in form order and skips unset values', () => {
    expect(
      profileChangeKeys({
        config: { method: 'POST', url: '', timeout: 30, headers: {} },
        retry: null,
      }),
    ).toEqual(['method', 'timeout']);
  });

  it('appends retry, included mappings, contracts and script', () => {
    expect(
      profileChangeKeys({
        config: { url: 'https://example.test' },
        retry: { maxAttempts: 3 },
        bundle: {
          mappings: { query: 'm1', body: '' },
          schemas: { output: 's1' },
          script: 'sc1',
        },
      }),
    ).toEqual(['url', 'retry', 'mapping:query', 'schema:output', 'script']);
  });

  it('returns nothing for a blank profile', () => {
    expect(profileChangeKeys({ config: {}, retry: null })).toEqual([]);
  });
});

describe('resolveProfileLocation', () => {
  const editing = { hasData: true, editing: true };

  it('defaults to identity', () => {
    expect(resolveProfileLocation(null, editing)).toEqual({
      area: 'identity',
      configSection: 'defaults',
      trustSection: 'test',
      canonical: 'identity',
    });
    expect(resolveProfileLocation('basics', editing).area).toBe('identity');
  });

  it('keeps legacy configuration aliases', () => {
    expect(resolveProfileLocation('defaults', editing)).toMatchObject({
      area: 'configuration',
      configSection: 'defaults',
      canonical: 'defaults',
    });
    expect(resolveProfileLocation('data', editing)).toMatchObject({
      area: 'configuration',
      configSection: 'data',
      canonical: 'data',
    });
    expect(resolveProfileLocation('contracts', editing)).toMatchObject({
      area: 'configuration',
      configSection: 'included',
      canonical: 'included',
    });
    expect(resolveProfileLocation('bundle', editing)).toMatchObject({
      area: 'configuration',
      configSection: 'included',
      canonical: 'included',
    });
  });

  it('maps trust aliases and falls back on create pages', () => {
    expect(resolveProfileLocation('used-by', editing)).toMatchObject({
      area: 'trust',
      trustSection: 'used-by',
      canonical: 'used-by',
    });
    expect(resolveProfileLocation('history', { hasData: true, editing: false })).toMatchObject({
      area: 'trust',
      trustSection: 'test',
      canonical: 'test',
    });
    expect(resolveProfileLocation('preview', editing).trustSection).toBe('preview');
  });

  it('falls back from data when the node type has no data section', () => {
    expect(resolveProfileLocation('data', { hasData: false, editing: false })).toMatchObject({
      area: 'configuration',
      configSection: 'defaults',
      canonical: 'defaults',
    });
  });
});

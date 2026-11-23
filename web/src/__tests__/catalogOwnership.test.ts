import { describe, expect, it } from 'vitest';
import { makeNode } from '../catalog';
import type { NodeDef } from '../types';

const httpDef: NodeDef = {
  type: 'http.request',
  typeVersion: 1,
  category: 'action',
  displayName: 'HTTP request',
  description: 'Requests an API',
  inputs: ['main'],
  outputs: ['main'],
  inputType: 'json',
  outputType: 'json',
};

describe('sparse node creation', () => {
  it('does not persist catalog defaults or suppress a later profile', () => {
    const node = makeNode(httpDef, []);
    expect(node.config).toEqual({});
    expect(node.profileOverrides).toEqual({});
  });
});

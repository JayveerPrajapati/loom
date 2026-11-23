import { describe, expect, it } from 'vitest';

/**
 * Grammar gate (audit Phase 0.3).
 *
 * Bans patterns that bypass the shared UI grammar. The baseline freezes the
 * existing occurrences so a new one fails review. An entry whose count drops
 * must also be lowered; the stale entry fails so the baseline cannot rot.
 *
 * `intentional` entries have a reviewed reason. `debt` entries are known gaps
 * owned by a later milestone; the test prints the remaining debt count.
 */
const PATTERNS = {
  confirm: /window\.confirm\s*\(/,
  prompt: /window\.prompt\s*\(/,
  silent: /\.catch\(\s*\(\s*\)\s*=>\s*(undefined|null)\s*\)/,
} as const;

type PatternName = keyof typeof PATTERNS;

interface Exception {
  counts: Partial<Record<PatternName, number>>;
  status: 'intentional' | 'debt';
  reason: string;
}

const BASELINE: Record<string, Exception> = {
  'src/App.tsx': {
    counts: { silent: 1 },
    status: 'intentional',
    reason: 'connector palette refresh; the previous defs stay usable',
  },
  'src/assetCache.ts': {
    counts: { silent: 1 },
    status: 'intentional',
    reason: 'stale-while-revalidate background refresh; a first-load failure is rethrown',
  },
  'src/components/CredentialsPage.tsx': {
    counts: { silent: 1 },
    status: 'intentional',
    reason: 'optional egress banner probe; failure hides the advisory banner',
  },
  'src/components/PythonStatusCard.tsx': {
    counts: { silent: 1 },
    status: 'intentional',
    reason: 'status probe; the disabled card is the fallback',
  },
  'src/components/dashboard/JobDetailPage.tsx': {
    counts: { silent: 2 },
    status: 'intentional',
    reason: 'optional run/workflow enrichment; the primary job load owns its error path',
  },
  'src/components/profileForm/useProfileAssets.ts': {
    counts: { silent: 4 },
    status: 'intentional',
    reason: 'optional picker asset loads; an empty picker degrades gracefully',
  },
  'src/components/runDetail/useEventDetail.ts': {
    counts: { silent: 2 },
    status: 'intentional',
    reason: 'optional blob and data-context enrichment',
  },
  'src/components/runDetail/useRunDetailData.ts': {
    counts: { silent: 1 },
    status: 'intentional',
    reason: 'optional workflow enrichment for run detail',
  },
  'src/components/useInspectorAssets.ts': {
    counts: { silent: 1 },
    status: 'intentional',
    reason: 'optional upstream blob preview',
  },
  'src/hooks/useWorkflowRun.ts': {
    counts: { silent: 1 },
    status: 'intentional',
    reason: 'background summary refresh after a mutation',
  },
  'src/hooks/useWorkspaceData.ts': {
    counts: { silent: 3 },
    status: 'intentional',
    reason: 'background profile/schema/mapping index refresh; summaries have a separate failure state',
  },
};

// Raw Vite glob: no Node filesystem types, and the same source tree the app
// compiles. The grammar test itself and all unit tests are excluded.
const SOURCES = import.meta.glob('../**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

function sourceKey(path: string): string {
  return path.replace(/^\.\.\//, 'src/');
}

function isTestPath(path: string): boolean {
  return path.includes('/__tests__/') || /\.test\.(ts|tsx)$/.test(path);
}

function matchLines(source: string, pattern: RegExp): number[] {
  const global = new RegExp(pattern.source, 'g');
  return [...source.matchAll(global)].map((match) => {
    const index = match.index ?? 0;
    return source.slice(0, index).split('\n').length;
  });
}

describe('grammar gate', () => {
  it('has no new banned patterns and no stale baseline entries', () => {
    const seen = new Set<string>();
    const violations: string[] = [];
    let debt = 0;

    const entries = Object.entries(SOURCES)
      .filter(([path]) => !isTestPath(path))
      .map(([path, source]) => [sourceKey(path), source] as const)
      .sort(([left], [right]) => left.localeCompare(right));

    for (const [key, source] of entries) {
      const exception = BASELINE[key];
      if (exception) seen.add(key);

      for (const name of Object.keys(PATTERNS) as PatternName[]) {
        const lines = matchLines(source, PATTERNS[name]);
        const allowed = exception?.counts[name] ?? 0;
        if (lines.length > allowed) {
          const where = lines.slice(allowed).map((line) => `${key}:${line}`).join(', ');
          violations.push(`new ${name} occurrence at ${where}`);
        } else if (lines.length < allowed) {
          violations.push(
            `stale ${name} baseline for ${key}: allowed ${allowed}, found ${lines.length}; lower the baseline`,
          );
        }
      }
    }

    for (const [key, exception] of Object.entries(BASELINE)) {
      if (!seen.has(key)) violations.push(`stale baseline file: ${key} no longer has a banned pattern`);
      if (exception.status === 'debt') debt += 1;
    }

    // U6 cleared the last debt entries; the count prints every run so a
    // relapse or a fully-clean tree is explicit.
    console.info(`[grammar] ${debt} file-level debt entries remain; see BASELINE in grammar.test.ts`);
    expect(violations, violations.join('\n')).toEqual([]);
  });
});

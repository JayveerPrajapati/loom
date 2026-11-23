import { describe, expect, it } from 'vitest';

/**
 * Action-error banner gate (audit U5).
 *
 * `banner error` is the one error presentation for action, validation and
 * sub-panel errors, and it must be a live region so asynchronous errors
 * announce. The only component allowed to render the class string is the
 * shared primitive `ActionError`; classified load failures go through
 * `AssetErrorState`, which computes the class and role per failure kind
 * and therefore never contains the literal.
 *
 * Like the grammar gate, this scans the raw source tree so a planted raw
 * banner fails review before it ships.
 */

// One home for the raw class string. If this moves, move the key below.
const PRIMITIVE_KEY = 'src/components/assets/ActionError.tsx';

// Matches a JSX opening tag whose className contains `banner error`,
// across attribute line breaks. Backtick template classNames
// (AssetErrorState) do not match the quoted form.
const BANNER_TAG = /<[A-Za-z][^<>]*?className=["'][^"']*banner error[^"']*["'][^<>]*?>/g;

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

describe('action-error banner gate', () => {
  it('renders banner error only through the shared ActionError primitive', () => {
    const violations: string[] = [];

    for (const [path, source] of Object.entries(SOURCES)) {
      const key = sourceKey(path);
      if (isTestPath(path) || key === PRIMITIVE_KEY) continue;

      for (const tag of source.match(BANNER_TAG) ?? []) {
        violations.push(
          `${key} renders a raw banner error (${tag.replace(/\s+/g, ' ')}); use ActionError from the shared kit`,
        );
      }
    }

    expect(violations, violations.join('\n')).toEqual([]);
  });

  it('the ActionError primitive stays an alert with the shared banner styling', () => {
    const source = SOURCES['../components/assets/ActionError.tsx'] ?? '';
    expect(source, 'ActionError.tsx is missing from the source glob').not.toBe('');
    expect(source, 'ActionError must keep the shared banner error styling').toContain(
      'banner error',
    );
    expect(source, 'ActionError must announce as a live region').toContain('role="alert"');
  });
});

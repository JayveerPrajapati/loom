import { autoArrange } from './layout';
import { defaultNodeName } from './catalog';
import type { Command } from './components/CommandPalette';
import type { Example, NodeDef, Workflow } from './types';

// Command list assembly (Phase 6 D6-1). Pure: every callback arrives as
// an argument so this stays trivially testable.
export function buildCommands(args: {
  routeName: string;
  workflow: Workflow | null;
  theme: 'light' | 'dark';
  examples: Example[];
  defs: NodeDef[];
  startRun: () => void;
  save: () => void;
  createBlank: () => void;
  apply: (mutator: (current: Workflow) => Workflow) => void;
  notify: (message: string, tone?: 'info' | 'error') => void;
  navigate: (path: string, options?: { replace?: boolean; force?: boolean }) => boolean;
  setTheme: (updater: (value: 'light' | 'dark') => 'light' | 'dark') => void;
  setAccountOpen: (open: boolean) => void;
  signOut: () => void;
  createFromExample: (example: Example) => void;
  addNode: (def: NodeDef, position?: { x: number; y: number }) => void;
  formatCount: (count: number, noun: string) => string;
}): Command[] {
  const {
    routeName, workflow, theme, examples, defs,
    startRun, save, createBlank, apply, notify, navigate,
    setTheme, setAccountOpen, signOut, createFromExample, addNode, formatCount,
  } = args;
  const list: Command[] = [];
  if (routeName === 'editor' && workflow) {
    list.push({
      id: 'run',
      label: 'Run workflow',
      hint: '⌘↵',
      group: 'Workflow',
      run: startRun,
    });
    list.push({
      id: 'save',
      label: 'Save workflow',
      hint: '⌘S',
      group: 'Workflow',
      run: save,
    });
    list.push({
      id: 'new',
      label: 'New blank workflow',
      group: 'Workflow',
      run: createBlank,
    });
    list.push({
      id: 'auto-arrange',
      label: 'Auto-arrange graph',
      hint: 'grid layout',
      group: 'Workflow',
      run: () => {
        apply((current) => autoArrange(current));
        notify('Graph auto-arranged — press ⌘Z to undo.');
      },
    });
  }
  for (const [label, path] of [
    ['Home', '/'],
    ['Workflows', '/workflows'],
    ['Jobs', '/jobs'],
    ['Runs', '/runs'],
    ['Tasks', '/tasks'],
    ['Credentials', '/credentials'],
  ] as const) {
    list.push({
      id: `go-${path}`,
      label: `Go to ${label}`,
      group: 'Go to',
      run: () => navigate(path),
    });
  }
  list.push({
    id: 'theme',
    label: `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`,
    group: 'Workspace',
    run: () => setTheme((value) => (value === 'dark' ? 'light' : 'dark')),
  });
  list.push({
    id: 'token',
    label: 'Account',
    group: 'Workspace',
    run: () => setAccountOpen(true),
  });
  list.push({
    id: 'sign-out',
    label: 'Sign out',
    group: 'Workspace',
    run: signOut,
  });
  for (const example of examples) {
    list.push({
      id: `example-${example.id}`,
      label: `Open example: ${example.name}`,
      hint: formatCount(example.workflow.nodes.length, 'node'),
      group: 'Examples',
      run: () => createFromExample(example),
    });
  }
  if (routeName === 'editor' && workflow) {
    for (const def of defs) {
      list.push({
        id: `node-${def.type}`,
        label: `Add ${defaultNodeName(def)}`,
        hint: def.category,
        // 6.5: surface the node-type explanation where nodes are added —
        // the palette is the other add path besides the library panel.
        description: def.description,
        group: 'Nodes',
        run: () => addNode(def),
      });
    }
  }
  return list;
}

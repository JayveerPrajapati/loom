import { useCallback, useEffect, useState } from 'react';
import { AppToast } from '../components/appChrome';
import { RunInputDialog } from '../components/RunInputDialog';
import { useDialogFocus } from '../useDialogFocus';
import { api } from '../api';
import type { Route, Toast } from '../appShared';

// App chrome clusters (Phase 6 D6-1 continuation): the small state
// machines that used to sit inline in App — toast notifications, the
// theme toggle, the waiting-approval badge poll, and the run-input
// dialog. Each returns its state plus, where useful, the JSX it owns.

export function useToast(route: Route) {
  const [toast, setToast] = useState<Toast | null>(null);

  const notify = useCallback((message: string, tone: 'info' | 'error' = 'info') => {
    setToast({ message, tone });
  }, []);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), 3600);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const node = toast ? (
    <AppToast message={toast.message} tone={toast.tone} aboveRunbar={route.name === 'editor'} />
  ) : null;

  return { notify, node };
}

export function useTheme() {
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    const saved = localStorage.getItem('loom.theme');
    if (saved === 'light' || saved === 'dark') return saved;
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  });

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('loom.theme', theme);
  }, [theme]);

  return { theme, setTheme };
}

export function useWaitingCount(principal: unknown) {
  const [waitingCount, setWaitingCount] = useState(0);

  const refreshWaiting = useCallback(() => {
    api
      .waitingRuns(1)
      .then((result) => setWaitingCount(result.count ?? 0))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!principal) {
      setWaitingCount(0);
      return;
    }
    refreshWaiting();
    const timer = window.setInterval(refreshWaiting, 30000);
    return () => window.clearInterval(timer);
  }, [principal, refreshWaiting]);

  return { waitingCount, refreshWaiting };
}

export function useRunInputDialog(args: {
  openRunInputWith: (open: (draft: string) => void) => void;
  submitRunInputWith: (
    draft: string,
    onError: (message: string) => void,
    onClose: () => void,
  ) => void;
  registerClose: (close: () => void) => void;
}) {
  const { openRunInputWith, submitRunInputWith, registerClose } = args;
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState('{}');
  const [error, setError] = useState('');
  const focusRef = useDialogFocus<HTMLDivElement>(open, () => setOpen(false));

  // App's closeDialogs (fired on session loss) needs to close this
  // dialog even though its state lives here.
  registerClose(() => setOpen(false));

  const openRunInput = useCallback(() => {
    openRunInputWith((next) => {
      setDraft(next);
      setError('');
      setOpen(true);
    });
  }, [openRunInputWith]);

  const submitRunInput = useCallback(() => {
    submitRunInputWith(draft, setError, () => setOpen(false));
  }, [draft, submitRunInputWith]);

  const node = open ? (
    <RunInputDialog
      draft={draft}
      error={error}
      focusRef={focusRef}
      onDraftChange={(value) => {
        setDraft(value);
        setError('');
      }}
      onClose={() => setOpen(false)}
      onSubmit={submitRunInput}
    />
  ) : null;

  return { openRunInput, submitRunInput, close: () => setOpen(false), node };
}

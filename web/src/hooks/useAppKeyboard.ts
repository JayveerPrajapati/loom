import { useEffect } from 'react';

// The global keyboard layer (Phase 6 D6-1): palette toggle, Escape
// cascade over dialogs, then editor shortcuts (save/run/undo/redo/r)
// — only on editor routes, never while typing in a field.
export function useAppKeyboard(args: {
  routeName: string;
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean | ((prev: boolean) => boolean)) => void;
  versionsOpen: boolean;
  accountOpen: boolean;
  closeRunInput: () => void;
  setVersionsOpen: (open: boolean) => void;
  setAccountOpen: (open: boolean) => void;
  save: () => Promise<boolean> | void;
  startRun: () => void;
  undo: () => void;
  redo: () => void;
}) {
  const {
    routeName, paletteOpen, setPaletteOpen,
    setVersionsOpen, setAccountOpen, closeRunInput,
    save, startRun, undo, redo,
  } = args;
  const versionsOpen = args.versionsOpen;
  const accountOpen = args.accountOpen;
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const modifier = event.metaKey || event.ctrlKey;
      const target = event.target as HTMLElement | null;
      const editing =
        !!target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.tagName === 'SELECT' ||
          target.isContentEditable);

      if (modifier && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen((open) => !open);
        return;
      }
      if (event.key === 'Escape') {
        // Priority cascade: the topmost dialog only.
        if (paletteOpen) setPaletteOpen(false);
        else if (versionsOpen) setVersionsOpen(false);
        else if (accountOpen) setAccountOpen(false);
        else closeRunInput();
        return;
      }
      if (routeName !== 'editor') return;
      if (modifier && event.key.toLowerCase() === 's') {
        event.preventDefault();
        void save();
        return;
      }
      if (modifier && event.key === 'Enter') {
        event.preventDefault();
        startRun();
        return;
      }
      if (modifier && event.key.toLowerCase() === 'z' && !editing) {
        event.preventDefault();
        if (event.shiftKey) redo();
        else undo();
        return;
      }
      if (!editing && !modifier && event.key.toLowerCase() === 'r') {
        event.preventDefault();
        startRun();
        return;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [paletteOpen, redo, routeName, save, startRun, undo]);
}

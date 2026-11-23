import type { Dispatch, SetStateAction } from 'react';
import type { AuthUser } from '../../types';

export interface SettingsMenuProps {
  principal: AuthUser | null;
  navigate: (path: string, options?: { replace?: boolean; force?: boolean }) => boolean | void;
  theme: 'light' | 'dark';
  setTheme: Dispatch<SetStateAction<'light' | 'dark'>>;
  setAccountOpen: (open: boolean) => void;
  signOut: () => void | Promise<void>;
  setPaletteOpen: (open: boolean) => void;
}

/** The app-level menu, kept separate from the editor's More menu on
 *  purpose: it exists on every route, while More is workflow-scoped.
 *  Audit 3D's "clearly grouped equivalent". */
export function SettingsMenu({
  principal,
  navigate,
  theme,
  setTheme,
  setAccountOpen,
  signOut,
  setPaletteOpen,
}: SettingsMenuProps) {
  return (
    <details className="settings-menu">
      <summary className="button ghost">Settings</summary>
      <div className="settings-menu-list">
        {principal?.role === 'admin' && (
          <button onClick={() => navigate('/credentials')}>Credentials</button>
        )}
        <button onClick={() => navigate('/profiles')}>Profiles</button>
        <button onClick={() => navigate('/schemas')}>Data contracts</button>
        <button onClick={() => navigate('/mappings')}>Mappings</button>
        <button onClick={() => setTheme((value) => (value === 'dark' ? 'light' : 'dark'))}>
          {theme === 'dark' ? 'Light theme' : 'Dark theme'}
        </button>
        <button onClick={() => setAccountOpen(true)}>Account</button>
        <button onClick={() => void signOut()}>Sign out</button>
        <button onClick={() => setPaletteOpen(true)}>Command palette</button>
      </div>
    </details>
  );
}

import { ActionError } from './assets/ActionError';
import { useCallback, useEffect, useState } from 'react';
import { api, ApiError } from '../api';
import type { APITokenInfo, AuthUser } from '../types';
import { useDialogFocus } from '../useDialogFocus';

const ROLES: AuthUser['role'][] = ['viewer', 'operator', 'editor', 'admin'];

interface Props {
  principal: AuthUser;
  onClose: () => void;
  onSignOut: () => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}

function message(reason: unknown): string {
  if (reason instanceof ApiError) return reason.message;
  return reason instanceof Error ? reason.message : String(reason);
}

export function AdminPanel({ principal, onClose, onSignOut, onNotify }: Props) {
  const isAdmin = principal.role === 'admin';
  const dialogRef = useDialogFocus<HTMLDivElement>(true, onClose);
  const [users, setUsers] = useState<AuthUser[]>([]);
  const [tokens, setTokens] = useState<APITokenInfo[]>([]);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState('');

  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');

  const [tokenName, setTokenName] = useState('cli');
  const [tokenRole, setTokenRole] = useState<AuthUser['role']>(principal.role);
  const [newToken, setNewToken] = useState('');

  const [userEmail, setUserEmail] = useState('');
  const [userName, setUserName] = useState('');
  const [userRole, setUserRole] = useState<AuthUser['role']>('viewer');
  const [userPassword, setUserPassword] = useState('');

  const load = useCallback(async () => {
    try {
      const [tokenResult, userResult] = await Promise.all([
        api.listTokens(),
        isAdmin ? api.listUsers() : Promise.resolve({ users: [] as AuthUser[] }),
      ]);
      setTokens(tokenResult.tokens ?? []);
      setUsers(userResult.users ?? []);
    } catch (reason) {
      setError(message(reason));
    }
  }, [isAdmin]);

  useEffect(() => {
    void load();
  }, [load]);

  const run = async (key: string, action: () => Promise<void>) => {
    setBusy(key);
    setError('');
    setNotice('');
    try {
      await action();
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy('');
    }
  };

  const changePassword = (event: React.FormEvent) => {
    event.preventDefault();
    void run('password', async () => {
      await api.changePassword(current, next);
      setCurrent('');
      setNext('');
      setNotice('Password updated. Other sessions were signed out.');
    });
  };

  const createToken = (event: React.FormEvent) => {
    event.preventDefault();
    void run('token', async () => {
      const result = await api.createToken({ name: tokenName, role: tokenRole });
      setNewToken(result.token);
      setNotice('Token created — copy it now, it is shown once.');
      await load();
    });
  };

  const createUser = (event: React.FormEvent) => {
    event.preventDefault();
    void run('user', async () => {
      await api.createUser({
        email: userEmail,
        name: userName,
        role: userRole,
        password: userPassword,
      });
      setUserEmail('');
      setUserName('');
      setUserPassword('');
      await load();
      setNotice('User created.');
    });
  };

  return (
    <div
      className="overlay"
      onMouseDown={(event) => event.target === event.currentTarget && onClose()}
    >
      <div
        ref={dialogRef}
        className="dialog admin-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Account and users"
        tabIndex={-1}
      >
        <div className="run-node-head">
          <div>
            <h2>{principal.name || principal.email}</h2>
            <p className="muted small">
              {principal.email} · <strong>{principal.role}</strong>
            </p>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="Close account">
            ×
          </button>
        </div>

        {error && <ActionError>{error}</ActionError>}
        {notice && <p className="banner success">{notice}</p>}

        <section className="inspector-section">
          <h3>Change password</h3>
          <form onSubmit={changePassword} className="field-stack">
            <label className="field" htmlFor="account-current">
              <span className="field-label">Current password</span>
              <input
                id="account-current"
                className="text-input"
                type="password"
                autoComplete="current-password"
                value={current}
                onChange={(event) => setCurrent(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="account-next">
              <span className="field-label">New password</span>
              <input
                id="account-next"
                className="text-input"
                type="password"
                autoComplete="new-password"
                value={next}
                onChange={(event) => setNext(event.target.value)}
              />
            </label>
            <button className="button primary" type="submit" disabled={busy === 'password'}>
              {busy === 'password' ? 'Saving…' : 'Update password'}
            </button>
          </form>
        </section>

        <section className="inspector-section">
          <h3>API tokens</h3>
          <p className="field-hint">
            Tokens are shown once. Use them as <code>Authorization: Bearer …</code> from the CLI
            or CI.
          </p>
          {newToken && (
            <pre className="json-view" aria-label="New API token">
              {newToken}
            </pre>
          )}
          <form onSubmit={createToken} className="field-stack">
            <label className="field" htmlFor="token-name">
              <span className="field-label">Label</span>
              <input
                id="token-name"
                className="text-input"
                value={tokenName}
                onChange={(event) => setTokenName(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="token-role">
              <span className="field-label">Role</span>
              <select
                id="token-role"
                className="text-input"
                value={tokenRole}
                onChange={(event) => setTokenRole(event.target.value as AuthUser['role'])}
              >
                {ROLES.filter((role) => roleRank(role) <= roleRank(principal.role)).map((role) => (
                  <option key={role} value={role}>
                    {role}
                  </option>
                ))}
              </select>
            </label>
            <button className="button subtle" type="submit" disabled={busy === 'token'}>
              {busy === 'token' ? 'Creating…' : 'Create token'}
            </button>
          </form>
          {tokens.length > 0 && (
            <ul className="admin-list" aria-label="API tokens">
              {tokens.map((token) => (
                <li key={token.id}>
                  <span>
                    {token.name} · {token.role} · {token.revokedAt ? 'revoked' : 'active'}
                  </span>
                  {!token.revokedAt && (
                    <button
                      className="button subtle"
                      onClick={() =>
                        void run('revoke-' + token.id, async () => {
                          await api.revokeToken(token.id);
                          await load();
                          onNotify?.(`Token “${token.name}” revoked.`);
                        })
                      }
                    >
                      Revoke
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>

        {isAdmin && (
          <section className="inspector-section">
            <h3>Users</h3>
            <form onSubmit={createUser} className="field-stack">
              <label className="field" htmlFor="user-email">
                <span className="field-label">Email</span>
                <input
                  id="user-email"
                  className="text-input"
                  type="email"
                  value={userEmail}
                  onChange={(event) => setUserEmail(event.target.value)}
                />
              </label>
              <label className="field" htmlFor="user-name">
                <span className="field-label">Name</span>
                <input
                  id="user-name"
                  className="text-input"
                  value={userName}
                  onChange={(event) => setUserName(event.target.value)}
                />
              </label>
              <label className="field" htmlFor="user-role">
                <span className="field-label">Role</span>
                <select
                  id="user-role"
                  className="text-input"
                  value={userRole}
                  onChange={(event) => setUserRole(event.target.value as AuthUser['role'])}
                >
                  {ROLES.map((role) => (
                    <option key={role} value={role}>
                      {role}
                    </option>
                  ))}
                </select>
              </label>
              <label className="field" htmlFor="user-password">
                <span className="field-label">Temporary password</span>
                <input
                  id="user-password"
                  className="text-input"
                  type="password"
                  autoComplete="new-password"
                  value={userPassword}
                  onChange={(event) => setUserPassword(event.target.value)}
                />
              </label>
              <button className="button primary" type="submit" disabled={busy === 'user'}>
                {busy === 'user' ? 'Creating…' : 'Create user'}
              </button>
            </form>

            <ul className="admin-list" aria-label="Users">
              {users.map((user) => (
                <li key={user.id}>
                  <span>
                    {user.email} · {user.role}
                    {user.disabled ? ' · disabled' : ''}
                  </span>
                  <span className="admin-actions">
                    <select
                      className="text-input compact"
                      aria-label={`Role for ${user.email}`}
                      value={user.role}
                      onChange={(event) =>
                        void run('role-' + user.id, async () => {
                          await api.updateUser(user.id, { role: event.target.value });
                          await load();
                          onNotify?.(`${user.email} is now ${event.target.value}.`);
                        })
                      }
                    >
                      {ROLES.map((role) => (
                        <option key={role} value={role}>
                          {role}
                        </option>
                      ))}
                    </select>
                    <button
                      className="button subtle"
                      onClick={() =>
                        void run('disable-' + user.id, async () => {
                          await api.updateUser(user.id, { disabled: !user.disabled });
                          await load();
                          onNotify?.(user.disabled ? `${user.email} enabled.` : `${user.email} disabled.`);
                        })
                      }
                    >
                      {user.disabled ? 'Enable' : 'Disable'}
                    </button>
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )}

        <div className="dialog-actions">
          <button className="button subtle" onClick={onSignOut}>
            Sign out
          </button>
          <button className="button primary" onClick={onClose}>
            Done
          </button>
        </div>
      </div>
    </div>
  );
}

function roleRank(role: string): number {
  switch (role) {
    case 'admin':
      return 4;
    case 'editor':
      return 3;
    case 'operator':
      return 2;
    case 'viewer':
      return 1;
    default:
      return 0;
  }
}

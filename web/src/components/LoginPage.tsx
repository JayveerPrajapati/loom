import { ActionError } from './assets/ActionError';
import { useEffect, useId, useRef, useState } from 'react';
import { api, ApiError } from '../api';
import type { AuthResponse } from '../types';

interface Props {
  onSuccess: (response: AuthResponse) => void;
  message?: string;
  /** 'notice' renders the message as informational (session expiry); the
   * default 'error' tone is for genuine sign-in failures. */
  messageTone?: 'error' | 'notice';
  /** True when the server re-enters anonymous admin after sign-out
   * (--allow-anonymous); the App probes this only on the sign-out path so
   * startup makes no redundant /api/auth/me call. */
  anonymous?: boolean;
}

function countdownMessage(secs: number): string {
  const minutes = Math.ceil(secs / 60);
  return `Try again in about ${minutes} minute${minutes === 1 ? '' : 's'}.`;
}

export function LoginPage({ onSuccess, message, messageTone, anonymous }: Props) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [lockoutSecs, setLockoutSecs] = useState(0);
  const [unlocked, setUnlocked] = useState(false);
  const locked = lockoutSecs > 0;
  // The sign-in error is the only state wired to the fields: the
  // session-expiry notice and the anonymous notice are informational, and
  // the lockout is its own state.
  const errorId = useId();
  const describedBy = error ? errorId : undefined;
  const wasLocked = useRef(false);

  useEffect(() => {
    if (!locked) return;
    const timer = setInterval(() => setLockoutSecs((s) => Math.max(0, s - 1)), 1000);
    return () => clearInterval(timer);
  }, [locked]);

  useEffect(() => {
    if (locked) {
      wasLocked.current = true;
      setUnlocked(false);
    } else if (wasLocked.current) {
      wasLocked.current = false;
      setUnlocked(true);
    }
  }, [locked]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError('');
    setUnlocked(false);
    try {
      const response = await api.login(email.trim(), password);
      onSuccess(response);
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 429) {
        setError('');
        setLockoutSecs(reason.retryAfterSec ?? 300);
      } else {
        setError(
          reason instanceof ApiError && reason.status === 403
            ? 'This account is disabled. Ask an administrator to re-enable it.'
            : 'Invalid email or password.',
        );
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <span className="brand-mark large" aria-hidden="true"><svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" strokeWidth="2.2" aria-hidden="true"><path d="M4 17c4-10 12 10 16 0" strokeLinecap="round" /></svg></span>
        <h1>Sign in</h1>
        <p className="muted">Use the account an administrator created for this workspace.</p>
        {anonymous && (
          <p className="banner notice">
            This server allows anonymous admin access (<code>--allow-anonymous</code>). Reloading
            any page, or going back after signing out, re-enters as anonymous admin.
          </p>
        )}
        {message && <p className={`banner ${messageTone === 'notice' ? 'notice' : 'error'}`}>{message}</p>}
        {error && (
          <ActionError id={errorId}>{error}</ActionError>
        )}
        {locked && (
          <ActionError testId="auth-lockout-alert">Too many attempts. Temporarily locked.</ActionError>
        )}
        {/* The countdown updates every second: it is deliberately not a
            live region, so screen readers hear the lockout alert once
            instead of a re-announcement on every tick. */}
        {locked && (
          <p className="muted small" data-testid="auth-lockout-countdown">
            {countdownMessage(lockoutSecs)}
          </p>
        )}
        {unlocked && (
          <p className="muted small" role="status" data-testid="auth-lockout-status">
            You can try again.
          </p>
        )}
        <form onSubmit={(event) => void submit(event)}>
          <label className="field" htmlFor="auth-email">
            <span className="field-label">Email</span>
            <input
              id="auth-email"
              className="text-input"
              type="email"
              autoFocus
              autoComplete="username"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              aria-invalid={error ? true : undefined}
              aria-describedby={describedBy}
            />
          </label>
          <label className="field" htmlFor="auth-password">
            <span className="field-label">Password</span>
            <input
              id="auth-password"
              className="text-input"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              aria-invalid={error ? true : undefined}
              aria-describedby={describedBy}
            />
          </label>
          <button className="button primary full" type="submit" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
        <p className="muted small">
          First time here? Create the first account from the server.
        </p>
        <details>
          <summary>First-time setup</summary>
          <p className="muted small">
            On the machine running Loom:{' '}
            <code>loom user create --email you@example.com --role admin --password-stdin</code>
          </p>
        </details>
      </div>
    </div>
  );
}

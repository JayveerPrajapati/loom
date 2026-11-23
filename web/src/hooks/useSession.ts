import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../api';
import type { AuthResponse, AuthUser, SubprocessNodes } from '../types';

// The session/connectivity cluster (Phase 6 D6-1): sign-in state, the
// auth-required event handler with its expiry notice, online/offline
// tracking and the subprocess capability from /healthz. Sign-out
// re-probes /api/auth/me because anonymous servers re-enter anonymous
// admin — startup never makes that call.
export function useSession(closeDialogs: () => void) {
  const [phase, setPhase] = useState<'loading' | 'ready' | 'error'>('loading');
  const [authRequired, setAuthRequired] = useState(false);
  const [authError, setAuthError] = useState('');
  const [authExpired, setAuthExpired] = useState(false);
  // Mirrors authExpired for the auth-required event handler: parallel 401s
  // fire the handler more than once, and only the first sets the notice.
  const authExpiredRef = useRef(false);
  const [anonymousLogin, setAnonymousLogin] = useState(false);
  const [bootError, setBootError] = useState('');
  const [principal, setPrincipal] = useState<AuthUser | null>(null);
  // Read by the auth-required handler to tell the initial expected 401
  // (fresh load, nobody signed in) from a mid-session expiry.
  const principalRef = useRef<AuthUser | null>(null);
  const [online, setOnline] = useState(() => typeof navigator === 'undefined' || navigator.onLine);
  // code.python capability from /healthz — null when unknown (healthz not yet
  // fetched or an older server without subprocessNodes), so warnings only
  // fire on an explicit disabled state, never on missing data.
  const [subprocess, setSubprocess] = useState<SubprocessNodes | null>(null);
  const [authChecked, setAuthChecked] = useState(false);

  const checkConnection = useCallback(async () => {
    try {
      const health = await api.healthz();
      setSubprocess(health.subprocessNodes ?? null);
      setOnline(true);
    } catch {
      setOnline(false);
    }
  }, []);

  useEffect(() => {
    const onConnectivity = (event: Event) => {
      const detail = (event as CustomEvent<{ online?: boolean }>).detail;
      if (typeof detail?.online === 'boolean') setOnline(detail.online);
    };
    const onAuthRequired = () => {
      // The initial expected 401 (fresh load, nobody signed in) renders no
      // banner at all; only a mid-session expiry gets a message, and a
      // notice-tone one, because expiring is normal operation — not an
      // error the user needs to recover from. A second 401 from the same
      // expiry (parallel fetches both fail) must not clear the notice.
      const expired = principalRef.current !== null;
      setPrincipal(null);
      principalRef.current = null;
      setAuthChecked(true);
      setAuthRequired(true);
      if (expired || !authExpiredRef.current) {
        setAuthError(expired ? 'Your session ended. Please sign in again.' : '');
        setAuthExpired(expired);
        if (expired) authExpiredRef.current = true;
      }
      setPhase('ready');
      // The app tree unmounts when the principal clears; App-level dialog
      // flags survive that, so a stale open dialog would remount over the
      // topbar after the next sign-in. Close everything here.
      closeDialogs();
    };
    const onOffline = () => setOnline(false);
    const onOnline = () => void checkConnection();
    window.addEventListener('loom:connectivity', onConnectivity);
    window.addEventListener('loom:auth-required', onAuthRequired);
    window.addEventListener('offline', onOffline);
    window.addEventListener('online', onOnline);
    return () => {
      window.removeEventListener('loom:connectivity', onConnectivity);
      window.removeEventListener('loom:auth-required', onAuthRequired);
      window.removeEventListener('offline', onOffline);
      window.removeEventListener('online', onOnline);
    };
  }, [checkConnection, closeDialogs]);

  useEffect(() => {
    if (online) return;
    const timer = window.setInterval(() => void checkConnection(), 4000);
    return () => window.clearInterval(timer);
  }, [checkConnection, online]);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      // The session may already be gone; the UI still returns to sign-in.
    }
    setPrincipal(null);
    principalRef.current = null;
    setAuthExpired(false);
    authExpiredRef.current = false;
    setAuthRequired(true);
    setAuthChecked(true);
    // Anonymous servers re-enter anonymous admin after sign-out; probe for
    // that only here so startup never makes a redundant /api/auth/me call.
    setAnonymousLogin(false);
    try {
      const session = await api.me();
      if (session.user?.id === 'anonymous') setAnonymousLogin(true);
    } catch {
      // Signed out for real; no probe result to show.
    }
  }, []);

  const onAuthenticated = useCallback(
    async (response: AuthResponse, reload: () => Promise<void>) => {
      setPrincipal(response.user);
      principalRef.current = response.user;
      setAuthExpired(false);
      authExpiredRef.current = false;
      // Defense in depth: no dialog may ride along across a sign-in.
      closeDialogs();
      setAuthRequired(false);
      setAuthError('');
      setAuthChecked(true);
      setPhase('loading');
      await reload();
    },
    [closeDialogs],
  );

  // Stable identity matters: useWorkflowLifecycle's loadInitial depends
  // on this object, and a fresh literal per render would re-fire the
  // boot effect forever. Every member is a stable setState or ref.
  const lifecycleHandlers = useMemo(
    () => ({
      setPhase,
      setBootError,
      setPrincipal,
      principalRef,
      setAuthChecked,
      authExpiredRef,
      setAuthRequired,
      setAuthError,
      setAuthExpired,
      setSubprocess,
    }),
    [],
  );

  return {
    lifecycleHandlers,
    phase,
    setPhase,
    authRequired,
    setAuthRequired,
    authError,
    setAuthError,
    authExpired,
    setAuthExpired,
    anonymousLogin,
    bootError,
    setBootError,
    principal,
    setPrincipal,
    principalRef,
    authChecked,
    setAuthChecked,
    authExpiredRef,
    online,
    subprocess,
    setSubprocess,
    checkConnection,
    signOut,
    onAuthenticated,
  };
}

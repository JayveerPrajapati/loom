import { useCallback, useEffect, useRef } from 'react';
import { parseRoute, type Route } from '../appShared';
import { confirmAction } from '../confirm';

// Route + history guard (Phase 6 D6-1): navigation with dirty-editor
// protection, the popstate mirror, and the legacy-URL redirect table.
// parseRoute matches pathnames only; navigate strips ?query and #hash so
// links like /mappings?nodeType=… still resolve to their route.
export function useRouteSync(
  route: Route,
  setRoute: (next: Route) => void,
  discardEditor: () => void,
  dirty: boolean,
) {
  // Set by the popstate guard when the user refuses to discard unsaved
  // editor changes on Back/Forward: the next popstate is the resulting
  // history bounce and must only re-read the URL.
  const skipPopRef = useRef(false);

  const confirmDiscard = useCallback(
    () =>
      confirmAction({
        title: 'Discard unsaved changes?',
        body: 'This workflow has unsaved changes. Leaving the editor discards them.',
        confirmLabel: 'Discard changes',
        cancelLabel: 'Keep editing',
      }),
    [],
  );

  const navigate = useCallback(
    (path: string, options?: { replace?: boolean; force?: boolean }) => {
      const go = () => {
        if (options?.replace) window.history.replaceState({}, '', path);
        else window.history.pushState({}, '', path);
        setRoute(parseRoute(path.split('?')[0].split('#')[0]));
      };
      if (!options?.force && route.name === 'editor' && dirty) {
        void confirmDiscard().then((confirmed) => {
          if (!confirmed) return;
          discardEditor();
          go();
        });
        return false;
      }
      go();
      return true;
    },
    [confirmDiscard, discardEditor, dirty, route.name, setRoute],
  );

  useEffect(() => {
    const onPopState = () => {
      if (skipPopRef.current) {
        skipPopRef.current = false;
        setRoute(parseRoute());
        return;
      }
      if (route.name === 'editor' && dirty) {
        void confirmDiscard().then((confirmed) => {
          if (!confirmed) {
            skipPopRef.current = true;
            window.history.go(1);
            return;
          }
          discardEditor();
          setRoute(parseRoute());
        });
        return;
      }
      setRoute(parseRoute());
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, [confirmDiscard, discardEditor, dirty, route.name, setRoute]);

  // Legacy URLs redirect in place: /templates → /workflows (UX Phase 0
  // terminology decision), the pre-merge /profiles/:nodeType family →
  // /hub/:nodeType (v4.1 E1), and the retired hub landing → /profiles
  // (UX Phase 2 D2-1). parseRoute keeps rendering the page so links
  // never dead-end; this effect then rewrites the address bar.
  useEffect(() => {
    if (!route.legacy) return;
    const id = route.id ? `/${encodeURIComponent(route.id)}` : '';
    const type = route.nodeType ? `/${encodeURIComponent(route.nodeType)}` : '';
    const target =
      route.name === 'workflows'
        ? '/workflows'
        : route.name === 'profiles'
          ? '/profiles'
          : route.name === 'workflow' || route.name === 'editor'
            ? `/workflows${id}${route.name === 'editor' ? '/edit' : ''}`
            : route.name === 'hub-node'
              ? `/hub${type}`
              : route.name === 'profile-new'
                ? `/hub${type}/new`
                : route.name === 'profile-edit'
                  ? `/hub${type}${route.profileId ? `/${encodeURIComponent(route.profileId)}` : ''}/edit`
                  : null;
    if (!target) return;
    window.history.replaceState({}, '', target);
    setRoute(parseRoute(target));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [route.legacy, route.name, route.id, route.nodeType, route.profileId]);

  // Unsaved-changes guard for tab close/reload while editing.
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirty]);

  return { navigate };
}

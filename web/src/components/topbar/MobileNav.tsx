import type { Route } from '../../appShared';
import type { AuthUser } from '../../types';
import { closeAncestorDetails } from './menu';
import { navItems } from './navItems';

export interface MobileNavProps {
  route: Route;
  navigate: (path: string, options?: { replace?: boolean; force?: boolean }) => boolean | void;
  waitingCount: number;
  principal: AuthUser | null;
}

/** The phone-width primary-nav trigger. Audit 3D keeps it beside the
 *  brand in the left zone; the list drops down from it. */
export function MobileNav({ route, navigate, waitingCount, principal }: MobileNavProps) {
  return (
    <details className="mobile-nav">
      <summary className="button subtle">Menu</summary>
      <nav className="mobile-nav-list" aria-label="Primary">
        {navItems(route, waitingCount, principal).map((item) => (
          <button
            key={item.key}
            className={item.active ? 'active' : ''}
            aria-current={item.active ? 'page' : undefined}
            onClick={(event) => {
              navigate(item.href);
              closeAncestorDetails(event.currentTarget);
            }}
          >
            {item.label}
            {item.badge ? <span className="nav-badge">{item.badge}</span> : null}
          </button>
        ))}
      </nav>
    </details>
  );
}

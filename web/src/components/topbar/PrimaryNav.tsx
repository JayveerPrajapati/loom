import type { Route } from '../../appShared';
import type { AuthUser } from '../../types';
import { navItems } from './navItems';

export interface PrimaryNavProps {
  route: Route;
  navigate: (path: string, options?: { replace?: boolean; force?: boolean }) => boolean | void;
  waitingCount: number;
  principal: AuthUser | null;
}

/** The brand and the desktop primary nav. Audit 3D: this is the top
 *  bar's left zone; under 900px the nav hides and the sibling
 *  MobileNav trigger takes over. */
export function PrimaryNav({ route, navigate, waitingCount, principal }: PrimaryNavProps) {
  return (
    <>
      <a
        className="brand"
        href="/"
        onClick={(event) => {
          event.preventDefault();
          navigate('/');
        }}
      >
        <span className="brand-mark" aria-hidden="true"><svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" strokeWidth="2.2" aria-hidden="true"><path d="M4 17c4-10 12 10 16 0" strokeLinecap="round" /></svg></span>
        <div>
          <strong>Loom</strong>
          <span>workflow studio</span>
        </div>
      </a>

      <nav className="topnav" aria-label="Primary">
        {navItems(route, waitingCount, principal).map((item) => (
          <a
            key={item.key}
            className={item.active ? 'active' : ''}
            href={item.href}
            onClick={(event) => {
              event.preventDefault();
              navigate(item.href);
            }}
          >
            {item.label}
            {item.badge ? <span className="nav-badge">{item.badge}</span> : null}
          </a>
        ))}
      </nav>
    </>
  );
}

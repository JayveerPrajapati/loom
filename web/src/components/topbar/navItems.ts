import type { Route } from '../../appShared';
import type { AuthUser } from '../../types';

export interface NavItem {
  key: string;
  label: string;
  href: string;
  active: boolean;
  /** Tasks waiting-approval count; rendered only when positive. */
  badge?: number;
}

/** One canonical item list shared by the desktop nav and the mobile
 *  menu so the two surfaces never drift apart. Tasks sits with the
 *  day-to-day flows; role-gated admin surfaces trail. */
export function navItems(
  route: Route,
  waitingCount: number,
  principal: AuthUser | null,
): NavItem[] {
  const name = route.name;
  const role = principal?.role;
  const items: NavItem[] = [
    { key: 'home', label: 'Home', href: '/', active: name === 'home' },
    {
      key: 'workflows',
      label: 'Workflows',
      href: '/workflows',
      active: name === 'workflows' || name === 'workflow',
    },
    {
      key: 'jobs',
      label: 'Jobs',
      href: '/jobs',
      active: name === 'jobs' || name === 'job' || name === 'job-new' || name === 'job-edit',
    },
    {
      key: 'runs',
      label: 'Runs',
      href: '/runs',
      active: name === 'runs' || name === 'run',
    },
    {
      key: 'tasks',
      label: 'Tasks',
      href: '/tasks',
      active: name === 'tasks',
      badge: waitingCount,
    },
  ];
  // Storage-operations campaign: the ops page is an editor surface;
  // the server enforces the role gate as well.
  if (role === 'editor' || role === 'admin') {
    items.push({ key: 'ops', label: 'Ops', href: '/ops', active: name === 'ops' });
  }
  if (role === 'admin') {
    items.push(
      { key: 'capabilities', label: 'Capabilities', href: '/capabilities', active: name === 'capabilities' },
      { key: 'audit', label: 'Audit', href: '/audit', active: name === 'audit' },
    );
  }
  items.push({
    key: 'profiles',
    label: 'Profiles',
    href: '/profiles',
    active:
      name === 'profiles' ||
      name === 'schemas' ||
      name === 'mappings' ||
      name === 'hub-node' ||
      name === 'profile-new' ||
      name === 'profile-edit',
  });
  return items;
}

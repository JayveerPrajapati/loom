import { createContext, useContext, type ReactNode } from 'react';

const PrincipalRoleContext = createContext<string | undefined>(undefined);

/** Makes the signed-in role available to read-only pages without prop drilling. */
export function PrincipalRoleProvider({
  role,
  children,
}: {
  role?: string;
  children: ReactNode;
}) {
  return <PrincipalRoleContext.Provider value={role}>{children}</PrincipalRoleContext.Provider>;
}

export function usePrincipalRole(): string | undefined {
  return useContext(PrincipalRoleContext);
}

/** Viewers get read-only pages; editors and admins may write. */
export function useCanWrite(): boolean {
  return usePrincipalRole() !== 'viewer';
}

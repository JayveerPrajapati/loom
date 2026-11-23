import { OriginBadge } from './assets/OriginBadge';
import { labelFor } from './inspectorHelpers';
import type { ConfigProfile, CredentialInfo, NodeDef, WorkflowNode } from '../types';

interface Props {
  def: NodeDef | undefined;
  node: WorkflowNode;
  credentials: CredentialInfo[];
  onCredentialChange: (key: string, value: string) => void;
  /** The pinned profile (UI-audit B2): its credentialRefs fill empty
   * node slots, displayed with an origin badge. */
  profile?: ConfigProfile | null;
  /** Inline validation text for a credential slot. */
  issueFor?: (key: string) => string | undefined;
}

// The shared Credentials section: one input per def.credentials slot,
// with a datalist of matching stored credentials and inline validation
// from the node issue center (Apple-Grade Phase 3 D3-2/D3-4).
export function InspectorCredentialsSection({
  def,
  node,
  credentials,
  onCredentialChange,
  profile,
  issueFor,
}: Props) {
  if ((def?.credentials?.length ?? 0) === 0) return null;
  const inherited = profile?.credentialRefs ?? {};
  return (
    <section className="inspector-section" id="credentials-section">
      <h3>Credentials</h3>
      <p className="field-hint">
        Reference a credential stored by the server. Optional credentials may be left blank.
      </p>
      {def?.credentials?.map((credential) => {
        const optional = (def.optionalCredentials ?? []).includes(credential);
        const nodeHas = node.credentials?.[credential] !== undefined && node.credentials?.[credential] !== '';
        const profileValue = inherited[credential];
        const issue = issueFor?.(`credential:${credential}`);
        return (
          <label className="field" key={credential} htmlFor={`cred-${credential}`}>
            <span className="field-label">
              {labelFor(credential)}
              {optional && <span className="muted small"> (optional)</span>}
              {!nodeHas && profileValue && (
                <OriginBadge state="inherited" fieldKey={`credential-${credential}`} />
              )}
            </span>
            <input
              id={`cred-${credential}`}
              className="text-input"
              list={`cred-list-${credential}`}
              value={node.credentials?.[credential] ?? profileValue ?? ''}
              placeholder={optional ? 'None' : credential}
              aria-invalid={issue ? true : undefined}
              onChange={(event) => onCredentialChange(credential, event.target.value)}
            />
            <datalist id={`cred-list-${credential}`}>
              {credentials
                .filter((item) => item.type === credential)
                .map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.id}
                  </option>
                ))}
            </datalist>
            {issue && (
              <p className="field-error" role="alert" data-testid={`field-error-credential-${credential}`}>
                {issue}
              </p>
            )}
          </label>
        );
      })}
    </section>
  );
}

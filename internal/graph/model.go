package graph

import (
	"loom/internal/egress"
	"loom/internal/schemas"
)

// Workflow is the on-disk, versioned graph. It is deliberately plain JSON:
// the same bytes are the editor document, the API payload and the test fixture.
type Workflow struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Version  int            `json:"version"`
	Nodes    []Node         `json:"nodes"`
	Edges    []Edge         `json:"edges"`
	Settings map[string]any `json:"settings,omitempty"`
	// Egress is the workflow's own outbound-destination allowlist
	// (operational-trust campaign, D7-2): `{allowlist: [
	// "https://api.example.com", ...]}`. Validated at save/publish and
	// enforced per run by the engine's host wrapper. Nil or empty means
	// the workflow imposes no restriction of its own — the global
	// operator policy still applies.
	Egress *egress.WorkflowPolicy `json:"egress,omitempty"`
	// InputSchemaRef names a data contract (id) the workflow's input item
	// is validated against — the contract callers of flow.subflow see.
	// Resolved into InputSchemaFields at publish (v2 plan, D4-style).
	InputSchemaRef string `json:"inputSchemaRef,omitempty"`
	// OutputSchemaRef names a data contract for the workflow's final
	// output — the subflow caller's downstream shape.
	OutputSchemaRef string `json:"outputSchemaRef,omitempty"`
	// InputSchemaFields/OutputSchemaFields are the publish-time resolved
	// contracts; the engine never needs the store to enforce them.
	InputSchemaFields  []schemas.Field `json:"inputSchemaFields,omitempty"`
	OutputSchemaFields []schemas.Field `json:"outputSchemaFields,omitempty"`
}

type Node struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	TypeVersion int               `json:"typeVersion"`
	Name        string            `json:"name"`
	Position    Position          `json:"position"`
	Config      map[string]any    `json:"config"`
	Credentials map[string]string `json:"credentials,omitempty"`
	// ProfileRef names a Configuration Hub profile (id) whose Profileable
	// config merges under this node's own config at publish time. The
	// resolved snapshot is what runs and replays (plan §18 D4).
	ProfileRef string `json:"profileRef,omitempty"`
	// ProfileOverrides records explicit ownership for keys this node
	// authored while the profile also supplies them. A missing map keeps
	// legacy equality-based inference; a present map (including an empty
	// one) is authoritative, so the field has no omitempty: dropping an
	// empty map would silently revert the node to legacy inference after
	// a JSON round-trip.
	ProfileOverrides map[string]bool `json:"profileOverrides"`
	// ConfigSources is publish-time provenance for Config keys, filled by
	// ResolveProfileRefs, the script resolver and ApplyCatalogDefaults.
	// It is advisory metadata for the UI and doctor; execution never reads
	// it. Values are the ConfigSource* constants.
	ConfigSources map[string]string `json:"configSources,omitempty"`
	// InputSchemaRef names a data contract (id) the trigger's inbound
	// payload is validated against (trigger.webhook; v2 plan D11).
	InputSchemaRef string `json:"inputSchemaRef,omitempty"`
	// OutputSchemaRef names a data contract checked against this node's
	// main output at run time (v2 plan D16: record + surface, never fail).
	OutputSchemaRef string       `json:"outputSchemaRef,omitempty"`
	Retry           *RetryPolicy `json:"retry,omitempty"`
	OnError         string       `json:"onError,omitempty"`
	Batch           *BatchPolicy `json:"batch,omitempty"`
	// InputSchemaFields/OutputSchemaFields are the publish-time resolved
	// contracts; the engine and webhook handler read these without store
	// access.
	InputSchemaFields  []schemas.Field `json:"inputSchemaFields,omitempty"`
	OutputSchemaFields []schemas.Field `json:"outputSchemaFields,omitempty"`
}

// ConfigSources values: where a published config key's effective value
// came from. "catalog" is a fallback that only fills keys no earlier
// layer supplied; "script" is script inlining at publish.
const (
	ConfigSourceNode    = "node"
	ConfigSourceProfile = "profile"
	ConfigSourceBundle  = "bundle"
	ConfigSourceCatalog = "catalog"
	ConfigSourceScript  = "script"
)

// KnownConfigSource reports whether value names a publish provenance.
func KnownConfigSource(value string) bool {
	switch value {
	case ConfigSourceNode, ConfigSourceProfile, ConfigSourceBundle,
		ConfigSourceCatalog, ConfigSourceScript:
		return true
	default:
		return false
	}
}

// SetConfigSource records one key's publish provenance. A nil map is
// allocated on first use.
func (n *Node) SetConfigSource(key, source string) {
	if n.ConfigSources == nil {
		n.ConfigSources = map[string]string{}
	}
	n.ConfigSources[key] = source
}

// BatchPolicy runs a node once per element of an array in the input item and
// aggregates the results into one item. ContinueOnItemError keeps the run
// going after an item fails; the failure is recorded as an item.failed event.
type BatchPolicy struct {
	ItemsPath           string `json:"itemsPath,omitempty"`
	ContinueOnItemError bool   `json:"continueOnItemError,omitempty"`
	Concurrency         int    `json:"concurrency,omitempty"`
}

// RetryPolicy overrides the engine defaults for one node. Zero values fall
// back to the engine's global attempts and backoff.
type RetryPolicy struct {
	MaxAttempts int `json:"maxAttempts,omitempty"`
	BackoffMs   int `json:"backoffMs,omitempty"`
	MaxDelayMs  int `json:"maxDelayMs,omitempty"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Edge struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
}

type Endpoint struct {
	Node string `json:"node"`
	Port string `json:"port"`
}

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"loom/internal/graph"
	"loom/internal/nodes"
)

// Configuration Hub profile store (plan §18): profiles are reusable,
// versioned, non-secret node configurations. Version rows are append-only —
// updates never rewrite history — and the usage index records which
// workflow version pinned which profile version at publish time.

var (
	ErrConfigProfileNotFound = errors.New("config profile not found")
	ErrStaleConfigProfile    = errors.New("config profile was modified concurrently")
	ErrConfigProfileInUse    = errors.New("config profile is referenced by published workflows")
)

// ConfigProfile is a reusable, versioned, non-secret node configuration.
// Version is the current version; Config holds only fields the node catalog
// marks Profileable, CredentialRefs reference credentials by ID.
type ConfigProfile struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	NodeType       string             `json:"nodeType"`
	Description    string             `json:"description,omitempty"`
	Version        int                `json:"version"`
	Config         map[string]any     `json:"config"`
	CredentialRefs map[string]string  `json:"credentialRefs,omitempty"`
	Retry          *graph.RetryPolicy `json:"retry,omitempty"`
	Tags           []string           `json:"tags,omitempty"`
	Owner          string             `json:"owner,omitempty"`
	// Bundle composes reusable mappings and schemas into the profile
	// (Profile-Centric UX Phase 1, P0-1). References live outside Config
	// so they validate as asset ids rather than node configuration, and a
	// bundle change creates a new profile version like any other change.
	Bundle *ProfileBundle `json:"bundle,omitempty"`
	// TestInput is the saved test fixture (Phase 5): a JSON sample item
	// that test surfaces prefill. It rides the version doc like any
	// other profile content.
	TestInput json.RawMessage `json:"testInput,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	// Visibility/CreatorID are governance columns on the entity table
	// (Phase 1 D-P0b): private assets are visible to their creator and
	// admins only. They never enter version docs.
	Visibility string `json:"visibility,omitempty"`
	CreatorID  string `json:"creatorId,omitempty"`
	// UsageCount is how many published workflow versions pin any version
	// of this profile; populated only by ListConfigProfiles.
	UsageCount int `json:"usageCount,omitempty"`
}

// ProfileBundle references reusable assets a profile composes. Mapping
// keys are mapping kinds ("item" for data.map profiles; body, query,
// headers, pathParams for http.request) validated against the node
// type's catalog MappingKinds; schema keys are "input" and "output".
// Script is a single script id whose language must match the profile's
// node type (javascript → code.run, python → code.python, Phase 4 D4-1).
type ProfileBundle struct {
	Mappings map[string]string `json:"mappings,omitempty"`
	Schemas  map[string]string `json:"schemas,omitempty"`
	Script   string            `json:"script,omitempty"`
}

// ConfigProfileVersion is one immutable snapshot in a profile's history.
type ConfigProfileVersion struct {
	ProfileID   string    `json:"profileId"`
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ConfigProfileUsage records a published workflow version pinned to a
// profile version — the delete guard and usage-count source.
type ConfigProfileUsage struct {
	ProfileID       string    `json:"profileId"`
	ProfileVersion  int       `json:"profileVersion"`
	WorkflowID      string    `json:"workflowId"`
	WorkflowVersion int       `json:"workflowVersion"`
	WorkflowName    string    `json:"workflowName,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// configProfileDoc is what a version row stores: everything that can change
// between versions, so any version is self-contained.
type configProfileDoc struct {
	Name           string             `json:"name"`
	Description    string             `json:"description,omitempty"`
	Config         map[string]any     `json:"config"`
	CredentialRefs map[string]string  `json:"credentialRefs,omitempty"`
	Retry          *graph.RetryPolicy `json:"retry,omitempty"`
	Tags           []string           `json:"tags,omitempty"`
	Owner          string             `json:"owner,omitempty"`
	// Bundle is omitted when nil: pre-Phase-1 version docs carry no
	// bundle key and must keep reading (and hashing) unchanged.
	Bundle *ProfileBundle `json:"bundle,omitempty"`
	// TestInput is the profile's saved test fixture (Phase 5): the
	// sample item test surfaces prefill. Omitted when nil so
	// pre-Phase-5 docs stay byte-identical.
	TestInput json.RawMessage `json:"testInput,omitempty"`
}

// httpParamGroupTargets maps the profileable parameter-group row arrays to
// the plain config key they compile into at publish (v4.1 E4). Rows merge
// per name: a node row replaces the profile's row with that name, and
// profile rows whose names the node did not set are appended. When the node
// sets the group's plain target object instead, that is an explicit
// whole-key override and the profile's rows for the group are dropped.
var httpParamGroupTargets = map[string]string{
	"bodyFields":   "body",
	"queryFields":  "query",
	"headerFields": "headers",
	"pathFields":   "pathParams",
}

// paramRowsList returns v as a row array; ok only for the array shape the
// typed editors write.
func paramRowsList(v any) ([]any, bool) {
	rows, ok := v.([]any)
	return rows, ok
}

// mergeParamRows merges node-level parameter rows over profile rows per row
// name: node rows keep their order and win, profile rows with names the node
// did not set follow.
func mergeParamRows(nodeRows, profileRows []any) []any {
	taken := map[string]bool{}
	for _, row := range nodeRows {
		if name, ok := paramRowName(row); ok {
			taken[name] = true
		}
	}
	merged := make([]any, 0, len(nodeRows)+len(profileRows))
	merged = append(merged, nodeRows...)
	for _, row := range profileRows {
		if name, ok := paramRowName(row); ok && taken[name] {
			continue
		}
		merged = append(merged, row)
	}
	return merged
}

func paramRowName(row any) (string, bool) {
	m, ok := row.(map[string]any)
	if !ok {
		return "", false
	}
	name, ok := m["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return "", false
	}
	return name, true
}

// injectBundleRefs folds a profile's bundle references into a node's
// empty reference slots (Phase 1 P0-2). Precedence per the plan (highest
// wins): the node's plain target object drops the group (checked here),
// node mappingRefs keep their group (checked here), and node-level
// *Fields rows simply overlay the compiled mapping rows later in
// ResolveMappingRefs — the same rule the v4.1 param-row merge uses.
func injectBundleRefs(n *graph.Node, bundle *ProfileBundle) {
	if n.Config == nil {
		n.Config = map[string]any{}
	}
	for group, ref := range bundle.Mappings {
		if ref == "" {
			continue
		}
		if group == MappingKindItem {
			if n.Type == "data.map" {
				if cur, _ := n.Config["mappingRef"].(string); cur == "" {
					n.Config["mappingRef"] = ref
				}
			}
			continue
		}
		// Parameter groups: the group key IS the plain target key
		// (body/query/headers/pathParams). A node that set the target
		// directly owns the whole group.
		if _, nodeHasTarget := n.Config[group]; nodeHasTarget {
			continue
		}
		groupRefs, _ := n.Config["mappingRefs"].(map[string]any)
		if groupRefs == nil {
			groupRefs = map[string]any{}
		}
		if cur, _ := groupRefs[group].(string); cur == "" {
			groupRefs[group] = ref
			n.Config["mappingRefs"] = groupRefs
		}
	}
	for slot, ref := range bundle.Schemas {
		if ref == "" {
			continue
		}
		switch slot {
		case "input":
			if n.InputSchemaRef == "" {
				n.InputSchemaRef = ref
			}
		case "output":
			if n.OutputSchemaRef == "" {
				n.OutputSchemaRef = ref
			}
		}
	}
	// Phase 4 D4-1: the bundle's script fills the node's empty scriptRef
	// slot. A node that already references a script (or carries local
	// source — checked later in ResolveScriptRefs) keeps its own.
	if bundle.Script != "" {
		if scriptRefLanguage(n.Type) != "" {
			if cur, _ := n.Config["scriptRef"].(string); cur == "" {
				n.Config["scriptRef"] = bundle.Script
			}
		}
	}
}

// scriptRefLanguage maps code node types to the script language they
// consume; "" for node types that cannot reference scripts.
func scriptRefLanguage(nodeType string) string {
	switch nodeType {
	case "code.run":
		return ScriptLanguageJavaScript
	case "code.python":
		return ScriptLanguagePython
	default:
		return ""
	}
}

// ResolveProfileRefs merges each node's profile config under its own config
// (the node's own value wins per key), folds the profile's credential
// references into slots the node has not filled, and applies the profile's
// retry policy when the node has none. Parameter-group row arrays
// (bodyFields/queryFields/headerFields/pathFields, v4.1 E4) merge per row
// name instead of per key. Used by the server at publish (plan §18 D4) and
// by the CLI's export command, so both share one semantics.
func (s *Store) ResolveProfileRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.ResolveProfileRefsWith(wf, NewLookupCache())
}

// ResolveProfileRefsWith is the request-scoped form: one cache is shared by
// validation and every publish resolver, so a repeated profile or bundle
// asset is read once per request instead of once per node.
func (s *Store) ResolveProfileRefsWith(wf *graph.Workflow, cache *LookupCache) (map[string]int, error) {
	if cache == nil {
		cache = NewLookupCache()
	}
	refs := map[string]int{}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.ProfileRef == "" {
			continue
		}
		profile, err := cache.ConfigProfile(s, n.ProfileRef)
		if err != nil {
			return nil, err
		}
		merged := map[string]any{}
		for key, value := range profile.Config {
			merged[key] = value
		}
		for key, value := range n.Config {
			// Explicit ownership is authoritative. An unmarked key follows
			// the profile; legacy nodes without metadata keep the old
			// value-wins compatibility rule.
			_, profileSuppliesKey := profile.Config[key]
			if n.ProfileOverrides == nil || keyIsExplicitOverride(n.ProfileOverrides, key) || !profileSuppliesKey {
				merged[key] = value
			}
		}
		// Profile-Centric UX Phase 1 (P0-2): bundle refs inject into the
		// node's own empty reference slots so the existing
		// ResolveMappingRefs/ResolveSchemaRefs compile and pin them exactly
		// like node-level refs. Everything the node set itself wins — the
		// plain target object, the group's mappingRef, the schema slots.
		beforeInjection := cloneConfig(n.Config)
		if profile.Bundle != nil {
			injectBundleRefs(n, profile.Bundle)
		}
		injected := changedConfigKeys(beforeInjection, n.Config)
		for key, value := range n.Config {
			_, profileSuppliesKey := profile.Config[key]
			if n.ProfileOverrides == nil || keyIsExplicitOverride(n.ProfileOverrides, key) || !profileSuppliesKey {
				merged[key] = value
			}
		}
		// v4.1 E4: parameter groups merge per row, not per key. The flat
		// merge above let the node's row array replace the profile's, or
		// vice versa; here the two arrays are interleaved by name. A
		// node-level plain target object (query/headers/pathParams/body)
		// is an explicit whole-key override, so the profile's rows for
		// that group are dropped instead of compiled over it.
		for fieldsKey, targetKey := range httpParamGroupTargets {
			profileRows, profileHasRows := paramRowsList(profile.Config[fieldsKey])
			if !profileHasRows {
				continue
			}
			_, nodeHasTarget := n.Config[targetKey]
			if nodeHasTarget {
				// The node said exactly what this group is; compiled
				// profile rows would overwrite that at publish.
				delete(merged, fieldsKey)
				continue
			}
			nodeRows, nodeHasRows := paramRowsList(n.Config[fieldsKey])
			if nodeHasRows {
				merged[fieldsKey] = mergeParamRows(nodeRows, profileRows)
			}
		}
		n.Config = merged
		// Record where each effective config key came from. The engine
		// ignores this metadata; the UI and doctor read it.
		sources := map[string]string{}
		for key := range merged {
			_, isGroup := httpParamGroupTargets[key]
			switch {
			case injected[key]:
				sources[key] = graph.ConfigSourceBundle
			case isGroup && keyIn(beforeInjection, key):
				// Mixed rows: the node supplied at least one row.
				sources[key] = graph.ConfigSourceNode
			case keyIn(profile.Config, key) &&
				!(n.ProfileOverrides != nil && keyIsExplicitOverride(n.ProfileOverrides, key)):
				sources[key] = graph.ConfigSourceProfile
			case keyIn(beforeInjection, key):
				sources[key] = graph.ConfigSourceNode
			case keyIn(profile.Config, key):
				sources[key] = graph.ConfigSourceProfile
			default:
				sources[key] = graph.ConfigSourceNode
			}
		}
		n.ConfigSources = sources
		for slot, cred := range profile.CredentialRefs {
			if n.Credentials == nil {
				n.Credentials = map[string]string{}
			}
			if _, taken := n.Credentials[slot]; !taken {
				n.Credentials[slot] = cred
			}
		}
		if profile.Retry != nil && n.Retry == nil {
			policy := *profile.Retry
			n.Retry = &policy
		}
		refs[profile.ID] = profile.Version
	}
	return refs, nil
}

// ApplyCatalogDefaults materializes effective-value fallbacks after all
// profile and asset resolution. Script and mapping resolvers intentionally
// see absent keys so their runtime defaults can win first.
func (s *Store) ApplyCatalogDefaults(wf *graph.Workflow) {
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		for key, value := range nodes.Defaults(n.Type) {
			if _, exists := n.Config[key]; !exists {
				if n.Config == nil {
					n.Config = map[string]any{}
				}
				n.Config[key] = value
				n.SetConfigSource(key, graph.ConfigSourceCatalog)
			}
		}
	}
}

// cloneConfig shallow-copies a config map so injection detection can
// compare before and after without aliasing the node's map.
func cloneConfig(config map[string]any) map[string]any {
	out := make(map[string]any, len(config))
	for key, value := range config {
		out[key] = value
	}
	return out
}

// changedConfigKeys returns the keys whose value differs between the two
// maps: added keys and keys whose (JSON) value changed.
func changedConfigKeys(before, after map[string]any) map[string]bool {
	changed := map[string]bool{}
	for key, value := range after {
		old, existed := before[key]
		if !existed || !jsonEqual(old, value) {
			changed[key] = true
		}
	}
	return changed
}

func keyIsExplicitOverride(overrides map[string]bool, key string) bool {
	return overrides[key]
}

// ProfileUpgradeChange is one key-level change an upgrade applies
// (UX Phase 2 D2-6). Keys use the profile-diff vocabulary:
// config.<key>, config.<fieldsKey>.<rowName>, credentials.<slot>, retry,
// bundle.mappings.<group>, bundle.schemas.<slot>.
type ProfileUpgradeChange struct {
	NodeID        string `json:"nodeId"`
	NodeName      string `json:"nodeName"`
	ProfileID     string `json:"profileId"`
	ProfileName   string `json:"profileName"`
	PinnedVersion int    `json:"pinnedVersion"`
	LatestVersion int    `json:"latestVersion"`
	Key           string `json:"key"`
	Before        string `json:"before"`
	After         string `json:"after"`
}

func jsonValue(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}

// pinnedProfileVersion reads the profile version the workflow's latest
// publish pinned (usage index). Zero means never published with it.
func (s *Store) pinnedProfileVersion(workflowID, profileID string) int {
	var pinned int
	_ = s.readRow(`SELECT profile_version FROM config_profile_usage
		WHERE workflow_id = ? AND profile_id = ?
		ORDER BY workflow_version DESC LIMIT 1`, workflowID, profileID).Scan(&pinned)
	return pinned
}

// UpgradeWorkflowProfiles re-resolves a workflow's profile refs against
// the profiles' CURRENT versions (UX Phase 2 D2-6). A publish inlines the
// resolved profile into the stored draft (plan §18 D4), so the node's
// stored values are indistinguishable from its own overrides — the
// upgrade infers ownership per key: a stored value that equals the
// PINNED version's value follows the profile (upgraded or dropped), a
// stored value that differs is the node's own override and stays.
// Never-published workflows have no pinned version: the draft is sparse,
// so the latest profile simply fills it.
func (s *Store) UpgradeWorkflowProfiles(wf *graph.Workflow) (*graph.Workflow, []ProfileUpgradeChange, error) {
	upgraded := new(graph.Workflow)
	if data, err := json.Marshal(wf); err != nil {
		return nil, nil, err
	} else if err := json.Unmarshal(data, upgraded); err != nil {
		return nil, nil, err
	}
	var plan []ProfileUpgradeChange
	for i := range upgraded.Nodes {
		n := &upgraded.Nodes[i]
		if n.ProfileRef == "" {
			continue
		}
		latest, err := s.GetConfigProfile(n.ProfileRef)
		if err != nil {
			return nil, nil, err
		}
		pinned := s.pinnedProfileVersion(wf.ID, n.ProfileRef)
		var old *ConfigProfile
		if pinned > 0 && pinned < latest.Version {
			old, err = s.GetConfigProfileVersion(n.ProfileRef, pinned)
			if err != nil {
				return nil, nil, err
			}
		}
		if old == nil && pinned >= latest.Version {
			// Already current: nothing to compute for this node.
			continue
		}
		oldConfig := map[string]any{}
		if old != nil {
			oldConfig = old.Config
		}
		nodeConfig := n.Config
		if nodeConfig == nil {
			nodeConfig = map[string]any{}
		}
		// A present ProfileOverrides map is authoritative. Legacy nodes
		// without it keep equality inference so existing workflows do not
		// change behavior before migration.
		explicitOwnership := n.ProfileOverrides != nil
		newConfig := map[string]any{}
		keys := map[string]bool{}
		for key := range oldConfig {
			keys[key] = true
		}
		for key := range latest.Config {
			keys[key] = true
		}
		for key := range nodeConfig {
			keys[key] = true
		}
		for _, key := range sortedKeys(keys) {
			// Parameter groups merge per row name with the same
			// ownership rule (v4.1 E4 vocabulary).
			if _, isGroup := httpParamGroupTargets[key]; isGroup {
				if _, nodeHasTarget := nodeConfig[httpParamGroupTargets[key]]; nodeHasTarget {
					// The node owns the whole group via its plain
					// target object; the profile's rows compile to
					// nothing here (publish drops them).
					continue
				}
				merged, changes := upgradeParamRows(key, oldConfig[key], latest.Config[key], nodeConfig[key])
				for _, change := range changes {
					plan = append(plan, ProfileUpgradeChange{
						NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
						PinnedVersion: pinned, LatestVersion: latest.Version,
						Key: change.key, Before: change.before, After: change.after,
					})
				}
				if rows, ok := merged.([]any); ok && len(rows) > 0 {
					newConfig[key] = rows
				}
				continue
			}
			inOld, inNew, inNode := keyIn(oldConfig, key), keyIn(latest.Config, key), keyIn(nodeConfig, key)
			switch {
			case inNode && explicitOwnership && n.ProfileOverrides[key]:
				// A deliberate override stays even when it equals either
				// the old or new profile value.
				newConfig[key] = nodeConfig[key]
			case inNode && explicitOwnership:
				// With authoritative metadata, an unmarked key is
				// inherited whenever the profile supplies it.
				if inNew {
					if !jsonEqual(nodeConfig[key], latest.Config[key]) {
						plan = append(plan, ProfileUpgradeChange{
							NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
							PinnedVersion: pinned, LatestVersion: latest.Version,
							Key: "config." + key, Before: jsonValue(nodeConfig[key]), After: jsonValue(latest.Config[key]),
						})
					}
					newConfig[key] = latest.Config[key]
				} else {
					// The profile no longer supplies the key; the value
					// becomes local rather than being deleted.
					newConfig[key] = nodeConfig[key]
				}
			case inNode && inOld && jsonEqual(nodeConfig[key], oldConfig[key]):
				// Legacy equality inference: the stored value came from
				// the pinned profile and follows the profile forward.
				if inNew {
					if !jsonEqual(nodeConfig[key], latest.Config[key]) {
						plan = append(plan, ProfileUpgradeChange{
							NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
							PinnedVersion: pinned, LatestVersion: latest.Version,
							Key: "config." + key, Before: jsonValue(nodeConfig[key]), After: jsonValue(latest.Config[key]),
						})
					}
					newConfig[key] = latest.Config[key]
				} else {
					plan = append(plan, ProfileUpgradeChange{
						NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
						PinnedVersion: pinned, LatestVersion: latest.Version,
						Key: "config." + key, Before: jsonValue(nodeConfig[key]), After: "",
					})
				}
			case inNode:
				newConfig[key] = nodeConfig[key]
			case inNew:
				// The profile fills a key the node never set. Against a
				// pinned version that is a visible upgrade; for a
				// never-published draft it is just the first publish's
				// normal fill, so no plan entry.
				newConfig[key] = latest.Config[key]
				if old != nil {
					plan = append(plan, ProfileUpgradeChange{
						NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
						PinnedVersion: pinned, LatestVersion: latest.Version,
						Key: "config." + key, Before: "", After: jsonValue(latest.Config[key]),
					})
				}
			}
		}
		n.Config = newConfig
		// Publish provenance describes the previous resolution; the
		// upgrade changes values, so the next publish recomputes it. A
		// stale source map must not survive into the upgraded draft.
		n.ConfigSources = nil
		if explicitOwnership {
			overrides := map[string]bool{}
			for key, explicit := range n.ProfileOverrides {
				inNew := keyIn(latest.Config, key)
				if explicit && inNew && keyIn(newConfig, key) {
					overrides[key] = true
				}
			}
			n.ProfileOverrides = overrides
		}

		// Credential slots: a slot filled from the pinned profile
		// follows the new one; slots the node set itself stay.
		oldCreds := map[string]string{}
		if old != nil {
			oldCreds = old.CredentialRefs
		}
		credSlots := map[string]bool{}
		for slot := range oldCreds {
			credSlots[slot] = true
		}
		for slot := range latest.CredentialRefs {
			credSlots[slot] = true
		}
		for slot := range n.Credentials {
			credSlots[slot] = true
		}
		for _, slot := range sortedStringKeys(credSlots) {
			nodeVal := n.Credentials[slot]
			oldVal, inOld := oldCreds[slot]
			newVal, inNew := latest.CredentialRefs[slot]
			if inOld && nodeVal == oldVal {
				// Slot came from the pinned profile.
				if inNew {
					if newVal != nodeVal {
						plan = append(plan, ProfileUpgradeChange{
							NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
							PinnedVersion: pinned, LatestVersion: latest.Version,
							Key: "credentials." + slot, Before: nodeVal, After: newVal,
						})
					}
					if n.Credentials == nil {
						n.Credentials = map[string]string{}
					}
					n.Credentials[slot] = newVal
				} else {
					plan = append(plan, ProfileUpgradeChange{
						NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
						PinnedVersion: pinned, LatestVersion: latest.Version,
						Key: "credentials." + slot, Before: nodeVal, After: "",
					})
					delete(n.Credentials, slot)
				}
			}
			// else: the node's own slot value stays.
		}

		// Retry: a policy copied from the pinned profile follows the new
		// one; the node's own policy (or none) stays.
		if n.Retry != nil && old != nil && old.Retry != nil && jsonEqual(n.Retry, old.Retry) {
			if latest.Retry != nil {
				policy := *latest.Retry
				n.Retry = &policy
			} else {
				n.Retry = nil
			}
			plan = append(plan, ProfileUpgradeChange{
				NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
				PinnedVersion: pinned, LatestVersion: latest.Version,
				Key: "retry", Before: jsonValue(old.Retry), After: jsonValue(latest.Retry),
			})
		}

		// Bundle refs: slots the pinned profile filled follow the new
		// bundle. Clear them first so injectBundleRefs fills fresh.
		if old != nil && old.Bundle != nil {
			for group, oldRef := range old.Bundle.Mappings {
				if oldRef == "" {
					continue
				}
				if group == MappingKindItem {
					if cur, _ := n.Config["mappingRef"].(string); cur == oldRef {
						delete(n.Config, "mappingRef")
					}
					continue
				}
				refs, _ := n.Config["mappingRefs"].(map[string]any)
				if cur, _ := refs[group].(string); cur == oldRef {
					delete(refs, group)
				}
			}
			for slot, oldRef := range old.Bundle.Schemas {
				if oldRef == "" {
					continue
				}
				if slot == "input" && n.InputSchemaRef == oldRef {
					n.InputSchemaRef = ""
				}
				if slot == "output" && n.OutputSchemaRef == oldRef {
					n.OutputSchemaRef = ""
				}
			}
			// Phase 4: the bundle's script follows the same rule. The
			// stored source is the old script's inlined value when the
			// node ran it (publish-resolved keys read node-origin); the
			// usage-index pin tells that apart from authored source, so
			// the new bundle's script re-resolves cleanly.
			if old.Bundle.Script != "" {
				if cur, _ := n.Config["scriptRef"].(string); cur == old.Bundle.Script {
					delete(n.Config, "scriptRef")
					if pinned := s.pinnedScriptVersion(wf.ID, old.Bundle.Script); pinned > 0 {
						if oldScript, err := s.GetScriptVersion(old.Bundle.Script, pinned); err == nil &&
							oldScript.Source == fmt.Sprint(n.Config["source"]) {
							delete(n.Config, "source")
						}
					}
				}
			}
		}
		if latest.Bundle != nil {
			injectBundleRefs(n, latest.Bundle)
		}

		// Plan entries for the bundle slots that changed.
		if latest.Bundle != nil {
			var oldMappings, oldSchemas map[string]string
			if old != nil && old.Bundle != nil {
				oldMappings, oldSchemas = old.Bundle.Mappings, old.Bundle.Schemas
			}
			for group, newRef := range latest.Bundle.Mappings {
				oldRef := oldMappings[group]
				if newRef == "" || newRef == oldRef {
					continue
				}
				plan = append(plan, ProfileUpgradeChange{
					NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
					PinnedVersion: pinned, LatestVersion: latest.Version,
					Key: "bundle.mappings." + group, Before: oldRef, After: newRef,
				})
			}
			for slot, newRef := range latest.Bundle.Schemas {
				oldRef := oldSchemas[slot]
				if newRef == "" || newRef == oldRef {
					continue
				}
				plan = append(plan, ProfileUpgradeChange{
					NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
					PinnedVersion: pinned, LatestVersion: latest.Version,
					Key: "bundle.schemas." + slot, Before: oldRef, After: newRef,
				})
			}
			var oldScript string
			if old != nil && old.Bundle != nil {
				oldScript = old.Bundle.Script
			}
			if latest.Bundle.Script != "" && latest.Bundle.Script != oldScript {
				plan = append(plan, ProfileUpgradeChange{
					NodeID: n.ID, NodeName: n.Name, ProfileID: latest.ID, ProfileName: latest.Name,
					PinnedVersion: pinned, LatestVersion: latest.Version,
					Key: "bundle.script", Before: oldScript, After: latest.Bundle.Script,
				})
			}
		}
	}
	return upgraded, plan, nil
}

func keyIn(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func jsonEqual(a, b any) bool {
	return jsonValue(a) == jsonValue(b)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedStringKeys(set map[string]bool) []string {
	return sortedKeys(set)
}

// paramUpgradeChange is one row-level change inside a parameter group.
type paramUpgradeChange struct {
	key, before, after string
}

// upgradeParamRows applies the per-key ownership rule to one parameter
// group at row granularity.
func upgradeParamRows(key string, oldRaw, newRaw, nodeRaw any) (any, []paramUpgradeChange) {
	oldRows, _ := paramRowsList(oldRaw)
	newRows, _ := paramRowsList(newRaw)
	nodeRows, _ := paramRowsList(nodeRaw)
	rowByName := func(rows []any) map[string]any {
		out := map[string]any{}
		for _, row := range rows {
			if name, ok := paramRowName(row); ok {
				out[name] = row
			}
		}
		return out
	}
	oldByName, newByName, nodeByName := rowByName(oldRows), rowByName(newRows), rowByName(nodeRows)
	names := map[string]bool{}
	for name := range oldByName {
		names[name] = true
	}
	for name := range newByName {
		names[name] = true
	}
	for name := range nodeByName {
		names[name] = true
	}
	var out []any
	var changes []paramUpgradeChange
	for _, name := range sortedKeys(names) {
		nodeRow, inNode := nodeByName[name]
		oldRow, inOld := oldByName[name]
		newRow, inNew := newByName[name]
		switch {
		case inNode && inOld && jsonEqual(nodeRow, oldRow):
			if inNew {
				if !jsonEqual(nodeRow, newRow) {
					changes = append(changes, paramUpgradeChange{key: "config." + key + "." + name, before: jsonValue(nodeRow), after: jsonValue(newRow)})
				}
				out = append(out, newRow)
			} else {
				changes = append(changes, paramUpgradeChange{key: "config." + key + "." + name, before: jsonValue(nodeRow), after: ""})
			}
		case inNode:
			out = append(out, nodeRow)
		case inNew:
			out = append(out, newRow)
		}
	}
	return out, changes
}

// CreateConfigProfile stores version 1 of a new profile.
func (s *Store) CreateConfigProfile(p *ConfigProfile, actor string) (ConfigProfile, error) {
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	p.Visibility = normalizeVisibility(p.Visibility)
	at := time.Now().UTC()
	doc := configProfileDoc{Name: p.Name, Description: p.Description, Config: p.Config, CredentialRefs: p.CredentialRefs, Retry: p.Retry, Tags: p.Tags, Owner: p.Owner, Bundle: p.Bundle, TestInput: p.TestInput}
	data, err := json.Marshal(doc)
	if err != nil {
		return ConfigProfile{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ConfigProfile{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO config_profiles (id, name, node_type, description, current_version, visibility, creator_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		p.ID, p.Name, p.NodeType, p.Description, p.Visibility, p.CreatorID, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		return ConfigProfile{}, err
	}
	if _, err := tx.Exec(`INSERT INTO config_profile_versions (profile_id, version, doc, content_hash, actor, created_at)
		VALUES (?, 1, ?, ?, ?, ?)`,
		p.ID, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return ConfigProfile{}, err
	}
	if err := replaceBundleRefs(tx, p.ID, p.Bundle); err != nil {
		return ConfigProfile{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConfigProfile{}, err
	}
	p.Version = 1
	p.CreatedAt = at
	p.UpdatedAt = at
	return *p, nil
}

// UpdateConfigProfile appends the next version. expectedVersion is the
// caller's current version: a mismatch means someone else wrote first and
// the update is rejected with ErrStaleConfigProfile (decision D3).
func (s *Store) UpdateConfigProfile(p *ConfigProfile, expectedVersion int, actor string) (ConfigProfile, error) {
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	at := time.Now().UTC()
	doc := configProfileDoc{Name: p.Name, Description: p.Description, Config: p.Config, CredentialRefs: p.CredentialRefs, Retry: p.Retry, Tags: p.Tags, Owner: p.Owner, Bundle: p.Bundle, TestInput: p.TestInput}
	data, err := json.Marshal(doc)
	if err != nil {
		return ConfigProfile{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ConfigProfile{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE config_profiles
		SET name = ?, description = ?, current_version = current_version + 1, updated_at = ?
		WHERE id = ? AND current_version = ?`,
		p.Name, p.Description, at.Format(time.RFC3339Nano), p.ID, expectedVersion)
	if err != nil {
		return ConfigProfile{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM config_profiles WHERE id = ?`, p.ID).Scan(&exists); err != nil {
			return ConfigProfile{}, err
		}
		if exists == 0 {
			return ConfigProfile{}, ErrConfigProfileNotFound
		}
		return ConfigProfile{}, ErrStaleConfigProfile
	}
	if err := tx.QueryRow(`SELECT current_version FROM config_profiles WHERE id = ?`, p.ID).Scan(&p.Version); err != nil {
		return ConfigProfile{}, err
	}
	if _, err := tx.Exec(`INSERT INTO config_profile_versions (profile_id, version, doc, content_hash, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.Version, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return ConfigProfile{}, err
	}
	if err := replaceBundleRefs(tx, p.ID, p.Bundle); err != nil {
		return ConfigProfile{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConfigProfile{}, err
	}
	p.UpdatedAt = at
	return *p, nil
}

func (s *Store) GetConfigProfile(id string) (*ConfigProfile, error) {
	var out ConfigProfile
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.node_type, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc
		FROM config_profiles p
		JOIN config_profile_versions v ON v.profile_id = p.id AND v.version = p.current_version
		WHERE p.id = ?`, id).Scan(&out.ID, &out.Name, &out.NodeType, &out.Description, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConfigProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanConfigProfileFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func scanConfigProfileFinish(out *ConfigProfile, created, updated string, doc []byte) error {
	var err error
	if out.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return err
	}
	if out.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return err
	}
	var d configProfileDoc
	if err := json.Unmarshal(doc, &d); err != nil {
		return err
	}
	out.Config = d.Config
	out.CredentialRefs = d.CredentialRefs
	out.Retry = d.Retry
	out.Tags = d.Tags
	out.Owner = d.Owner
	out.Bundle = d.Bundle
	out.TestInput = d.TestInput
	out.Visibility = normalizeVisibility(out.Visibility)
	return nil
}

// ListConfigProfiles returns every profile with its current doc plus its
// usage count from the publish-time usage index.
func (s *Store) ListConfigProfiles() ([]ConfigProfile, error) {
	rows, err := s.read(`SELECT p.id, p.name, p.node_type, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM config_profile_usage u WHERE u.profile_id = p.id)
		FROM config_profiles p
		JOIN config_profile_versions v ON v.profile_id = p.id AND v.version = p.current_version
		ORDER BY p.node_type, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigProfile
	usage := map[string]int{}
	for rows.Next() {
		var item ConfigProfile
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.NodeType, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return nil, err
		}
		if err := scanConfigProfileFinish(&item, created, updated, doc); err != nil {
			return nil, err
		}
		usage[item.ID] = count
		out = append(out, item)
	}
	// The usage count rides alongside, not inside, the versioned entity.
	for i := range out {
		out[i].UsageCount = usage[out[i].ID]
	}
	return out, rows.Err()
}

// ListConfigProfilesForType returns every profile for one node type —
// the hub page's per-type section (v3 plan D1).
func (s *Store) ListConfigProfilesForType(nodeType string) ([]ConfigProfile, error) {
	rows, err := s.read(`SELECT p.id, p.name, p.node_type, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM config_profile_usage u WHERE u.profile_id = p.id)
		FROM config_profiles p
		JOIN config_profile_versions v ON v.profile_id = p.id AND v.version = p.current_version
		WHERE p.node_type = ?
		ORDER BY p.name, p.id`, nodeType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigProfile
	usage := map[string]int{}
	for rows.Next() {
		var item ConfigProfile
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.NodeType, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return nil, err
		}
		if err := scanConfigProfileFinish(&item, created, updated, doc); err != nil {
			return nil, err
		}
		usage[item.ID] = count
		out = append(out, item)
	}
	for i := range out {
		out[i].UsageCount = usage[out[i].ID]
	}
	if out == nil {
		out = []ConfigProfile{}
	}
	return out, rows.Err()
}

// GetConfigProfileVersion reconstructs the profile as of one version.
func (s *Store) GetConfigProfileVersion(id string, version int) (*ConfigProfile, error) {
	var out ConfigProfile
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.node_type, p.description, v.version, p.visibility, p.creator_id, p.created_at, v.created_at, v.doc
		FROM config_profiles p
		JOIN config_profile_versions v ON v.profile_id = p.id
		WHERE p.id = ? AND v.version = ?`, id, version).
		Scan(&out.ID, &out.Name, &out.NodeType, &out.Description, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConfigProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanConfigProfileFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) ListConfigProfileVersions(id string) ([]ConfigProfileVersion, error) {
	rows, err := s.read(`SELECT profile_id, version, content_hash, actor, created_at
		FROM config_profile_versions WHERE profile_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigProfileVersion
	for rows.Next() {
		var item ConfigProfileVersion
		var created string
		if err := rows.Scan(&item.ProfileID, &item.Version, &item.ContentHash, &item.Actor, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteConfigProfile removes a profile and its version history, but only
// when the usage index shows no published workflow pinned it (decision D6).
func (s *Store) DeleteConfigProfile(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM config_profile_usage WHERE profile_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrConfigProfileInUse
	}
	res, err := tx.Exec(`DELETE FROM config_profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrConfigProfileNotFound
	}
	if _, err := tx.Exec(`DELETE FROM config_profile_versions WHERE profile_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM profile_bundle_refs WHERE profile_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// replaceBundleRefs rewrites the profile_bundle_refs index from the
// profile's current bundle (Phase 1 P0-4): the delete guard and the
// "referenced by" views read it. Runs inside the create/update
// transaction so the index can never lag the version doc.
func replaceBundleRefs(tx *sql.Tx, profileID string, bundle *ProfileBundle) error {
	if _, err := tx.Exec(`DELETE FROM profile_bundle_refs WHERE profile_id = ?`, profileID); err != nil {
		return err
	}
	if bundle == nil {
		return nil
	}
	for _, ref := range bundle.Mappings {
		if ref == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO profile_bundle_refs (profile_id, asset_kind, asset_id) VALUES (?, ?, ?)`,
			profileID, "mapping", ref); err != nil {
			return err
		}
	}
	for _, ref := range bundle.Schemas {
		if ref == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO profile_bundle_refs (profile_id, asset_kind, asset_id) VALUES (?, ?, ?)`,
			profileID, "schema", ref); err != nil {
			return err
		}
	}
	if bundle.Script != "" {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO profile_bundle_refs (profile_id, asset_kind, asset_id) VALUES (?, ?, ?)`,
			profileID, "script", bundle.Script); err != nil {
			return err
		}
	}
	return nil
}

// ProfilesReferencingAsset lists the profiles whose current bundle
// references the asset — the delete-guard error message and the
// used-by views.
func (s *Store) ProfilesReferencingAsset(kind, assetID string) ([]ConfigProfile, error) {
	rows, err := s.read(`SELECT p.id, p.name, p.node_type, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc
		FROM config_profiles p
		JOIN config_profile_versions v ON v.profile_id = p.id AND v.version = p.current_version
		JOIN profile_bundle_refs r ON r.profile_id = p.id
		WHERE r.asset_kind = ? AND r.asset_id = ?
		ORDER BY p.name, p.id`, kind, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigProfile
	for rows.Next() {
		var item ConfigProfile
		var created, updated string
		var doc []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.NodeType, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc); err != nil {
			return nil, err
		}
		if err := scanConfigProfileFinish(&item, created, updated, doc); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// RecordConfigProfileUsage pins a published workflow version to the profile
// version it resolved at publish time (decision D5).
func (s *Store) RecordConfigProfileUsage(profileID string, profileVersion int, workflowID string, workflowVersion int) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO config_profile_usage
		(profile_id, profile_version, workflow_id, workflow_version, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		profileID, profileVersion, workflowID, workflowVersion, now())
	return err
}

func (s *Store) ConfigProfileUsage(profileID string) ([]ConfigProfileUsage, error) {
	rows, err := s.read(`SELECT u.profile_id, u.profile_version, u.workflow_id, u.workflow_version, u.created_at, COALESCE(w.name, u.workflow_id)
		FROM config_profile_usage u LEFT JOIN workflows w ON w.id = u.workflow_id
		WHERE u.profile_id = ? ORDER BY u.created_at`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigProfileUsage
	for rows.Next() {
		var item ConfigProfileUsage
		var created string
		if err := rows.Scan(&item.ProfileID, &item.ProfileVersion, &item.WorkflowID, &item.WorkflowVersion, &created, &item.WorkflowName); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// ConfigProfileUsageCount returns how many published workflow versions
// reference the profile across all versions of it.
func (s *Store) ConfigProfileUsageCount(profileID string) (int, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM config_profile_usage WHERE profile_id = ?`, profileID).Scan(&count)
	return count, err
}

package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"loom/internal/graph"
)

// Ownership migration is explicit: PlanProfileOwnership classifies legacy
// nodes without writing anything, ApplyProfileOwnership stamps the plan with
// a backup and audit trail, and RollbackProfileOwnership restores it.
// Opening a data directory never rewrites a draft.

// OwnershipMigrationReportFile is the default report written by
// `loom ownership migrate --dry-run` inside the data directory.
const OwnershipMigrationReportFile = "ownership-migration.json"

type ownershipMigrationReport struct {
	GeneratedAt string                     `json:"generatedAt"`
	Workflows   int                        `json:"workflows"`
	Nodes       int                        `json:"nodes"`
	Inherited   int                        `json:"inherited"`
	Overrides   int                        `json:"overrides"`
	Changes     []ownershipMigrationChange `json:"changes"`
}

type ownershipMigrationChange struct {
	WorkflowID           string `json:"workflowId"`
	WorkflowName         string `json:"workflowName"`
	NodeID               string `json:"nodeId"`
	NodeType             string `json:"nodeType"`
	Key                  string `json:"key"`
	Decision             string `json:"decision"`
	Reason               string `json:"reason"`
	PinnedProfileVersion int    `json:"pinnedProfileVersion,omitempty"`
}

// Exported aliases keep the CLI and other callers on one report shape.
type OwnershipMigrationReport = ownershipMigrationReport
type OwnershipMigrationChange = ownershipMigrationChange

type ownershipWorkflowRow struct {
	id               string
	name             string
	publishedVersion int
	doc              []byte
}

// PlanProfileOwnership classifies legacy nodes with a profileRef. A value
// that matches the pinned profile and the last published snapshot is
// inherited; everything else is preserved as an override. Stored values are
// never changed, and nodes that already carry an ownership map are skipped.
// Nothing is written: the caller inspects the report and decides whether to
// call ApplyProfileOwnership.
func (s *Store) PlanProfileOwnership() (*ProfileOwnershipPlan, error) {
	rows, err := s.read(`SELECT id, name, published_version, doc FROM workflows ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("migrate profile ownership: %w", err)
	}
	var pending []ownershipWorkflowRow
	for rows.Next() {
		var row ownershipWorkflowRow
		if err := rows.Scan(&row.id, &row.name, &row.publishedVersion, &row.doc); err != nil {
			rows.Close()
			return nil, fmt.Errorf("migrate profile ownership: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("migrate profile ownership: %w", err)
	}
	rows.Close()
	var updates []ownershipWorkflowUpdate
	report := ownershipMigrationReport{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, row := range pending {
		id, name, publishedVersion, doc := row.id, row.name, row.publishedVersion, row.doc
		var wf graph.Workflow
		if err := json.Unmarshal(doc, &wf); err != nil {
			return nil, fmt.Errorf("plan ownership migration: workflow %q: %w", id, err)
		}
		publishedNodes := s.ownershipPublishedNodes(id, publishedVersion)
		var workflowChanges []OwnershipMigrationChange
		changedNodes := map[string]bool{}
		changed := false
		for i := range wf.Nodes {
			n := &wf.Nodes[i]
			if n.ProfileRef == "" || n.ProfileOverrides != nil {
				continue
			}
			pinned := s.ownershipPinnedVersion(id, n.ProfileRef)
			pinnedConfig, err := s.ownershipProfileConfig(n.ProfileRef, pinned)
			if err != nil {
				return nil, fmt.Errorf("migrate profile ownership: workflow %q node %q: %w", id, n.ID, err)
			}
			currentConfig, err := s.ownershipProfileConfig(n.ProfileRef, 0)
			if err != nil {
				return nil, fmt.Errorf("migrate profile ownership: workflow %q node %q: %w", id, n.ID, err)
			}
			overrides := map[string]bool{}
			record := func(key, decision, reason string) {
				if decision == "override" {
					report.Overrides++
				} else {
					report.Inherited++
				}
				workflowChanges = append(workflowChanges, OwnershipMigrationChange{
					WorkflowID: id, WorkflowName: name, NodeID: n.ID, NodeType: n.Type,
					Key: key, Decision: decision, Reason: reason, PinnedProfileVersion: pinned,
				})
			}
			for _, key := range sortedConfigKeys(n.Config) {
				value := n.Config[key]
				pinnedValue, pinnedSupplies := pinnedConfig[key]
				if pinnedConfig == nil {
					if currentConfig == nil {
						// No profile evidence at all: preserve every stored
						// value as the node's own.
						overrides[key] = true
						record(key, "override", "profile config unavailable; preserving the stored value")
						continue
					}
					if _, supplies := currentConfig[key]; supplies {
						overrides[key] = true
						record(key, "override", "no pinned profile version; preserving the stored value")
					}
					continue
				}
				if !pinnedSupplies {
					if _, supplies := currentConfig[key]; supplies {
						overrides[key] = true
						record(key, "override", "profile added the key after the pin; preserving the stored value")
					}
					continue
				}
				if !jsonEqual(value, pinnedValue) {
					overrides[key] = true
					record(key, "override", "value differs from the pinned profile")
					continue
				}
				publishedConfig, published := publishedNodes[n.ID]
				switch {
				case !published:
					overrides[key] = true
					record(key, "override", "no published snapshot evidence; ambiguous ownership")
				case !keyIn(publishedConfig, key):
					overrides[key] = true
					record(key, "override", "key was added after the last publish")
				case !jsonEqual(value, publishedConfig[key]):
					overrides[key] = true
					record(key, "override", "draft value was edited after the last publish")
				default:
					record(key, "inherited", "matches the pinned profile and the last published snapshot")
				}
			}
			n.ProfileOverrides = overrides
			changed = true
			changedNodes[n.ID] = true
			report.Nodes++
		}
		if changed {
			report.Workflows++
			report.Changes = append(report.Changes, workflowChanges...)
			data, err := json.Marshal(&wf)
			if err != nil {
				return nil, fmt.Errorf("plan ownership migration: workflow %q: %w", id, err)
			}
			updates = append(updates, ownershipWorkflowUpdate{
				ID:           id,
				Name:         name,
				OriginalDoc:  append([]byte(nil), doc...),
				MigratedDoc:  data,
				ChangedNodes: sortedBoolKeys(changedNodes),
				Changes:      workflowChanges,
			})
		}
	}
	return &ProfileOwnershipPlan{Report: report, updates: updates}, nil
}

// ownershipPinnedVersion returns the profile version the workflow's latest
// publish pinned, or 0 when it was never published with the profile.
func (s *Store) ownershipPinnedVersion(workflowID, profileID string) int {
	var pinned int
	err := s.readRow(`SELECT profile_version FROM config_profile_usage
		WHERE workflow_id = ? AND profile_id = ?
		ORDER BY workflow_version DESC LIMIT 1`, workflowID, profileID).Scan(&pinned)
	if err != nil {
		return 0
	}
	return pinned
}

// ownershipProfileConfig reads one profile version's config; version 0
// means the current version. A missing profile or version reads as nil so
// migration stays best-effort instead of failing a data-directory open.
func (s *Store) ownershipProfileConfig(profileID string, version int) (map[string]any, error) {
	var doc []byte
	var err error
	if version <= 0 {
		err = s.readRow(`SELECT doc FROM config_profile_versions
			WHERE profile_id = ? ORDER BY version DESC LIMIT 1`, profileID).Scan(&doc)
	} else {
		err = s.readRow(`SELECT doc FROM config_profile_versions
			WHERE profile_id = ? AND version = ?`, profileID, version).Scan(&doc)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stored configProfileDoc
	if err := json.Unmarshal(doc, &stored); err != nil {
		return nil, err
	}
	return stored.Config, nil
}

// ownershipPublishedNodes indexes the node configs of the workflow version
// its draft was last published from; an absent version is empty evidence.
func (s *Store) ownershipPublishedNodes(workflowID string, version int) map[string]map[string]any {
	out := map[string]map[string]any{}
	if version <= 0 {
		return out
	}
	var doc []byte
	if err := s.readRow(`SELECT doc FROM workflow_versions
		WHERE workflow_id = ? AND version = ?`, workflowID, version).Scan(&doc); err != nil {
		return out
	}
	var published graph.Workflow
	if err := json.Unmarshal(doc, &published); err != nil {
		return out
	}
	for _, n := range published.Nodes {
		out[n.ID] = n.Config
	}
	return out
}

func sortedConfigKeys(config map[string]any) []string {
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// scanProfileOwnership validates explicit ownership and publish provenance
// in drafts and published snapshots. Structural findings only — a legacy
// node with no profileOverrides map is compatible, not invalid.
func (s *Store) scanProfileOwnership(report *HealthReport) error {
	checkDoc := func(scope string, doc []byte) error {
		var wf graph.Workflow
		if err := json.Unmarshal(doc, &wf); err != nil {
			return fmt.Errorf("decode %s: %w", scope, err)
		}
		for _, n := range wf.Nodes {
			checkOwnership(report, scope, n)
		}
		return nil
	}
	rows, err := s.read(`SELECT id, doc FROM workflows ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var doc []byte
		if err := rows.Scan(&id, &doc); err != nil {
			rows.Close()
			return err
		}
		if err := checkDoc(fmt.Sprintf("workflow %q draft", id), doc); err != nil {
			rows.Close()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	versions, err := s.read(`SELECT workflow_id, version, doc FROM workflow_versions ORDER BY workflow_id, version`)
	if err != nil {
		return err
	}
	for versions.Next() {
		var id string
		var version int
		var doc []byte
		if err := versions.Scan(&id, &version, &doc); err != nil {
			versions.Close()
			return err
		}
		scope := fmt.Sprintf("workflow %q version %d", id, version)
		if err := checkDoc(scope, doc); err != nil {
			versions.Close()
			return err
		}
	}
	if err := versions.Err(); err != nil {
		versions.Close()
		return err
	}
	return versions.Close()
}

func checkOwnership(report *HealthReport, scope string, n graph.Node) {
	prefix := fmt.Sprintf("%s node %q", scope, n.ID)
	if n.ProfileOverrides != nil {
		if len(n.ProfileOverrides) > 0 && n.ProfileRef == "" {
			report.ownershipFailure("%s: profileOverrides present without profileRef", prefix)
		}
		for _, key := range sortedBoolKeys(n.ProfileOverrides) {
			if !n.ProfileOverrides[key] {
				report.ownershipFailure("%s: profileOverrides[%q] is false; inherited keys are omitted", prefix, key)
			}
			if _, ok := n.Config[key]; !ok {
				report.ownershipFailure("%s: profileOverrides[%q] marks a key the node does not store", prefix, key)
			}
		}
	}
	for _, key := range sortedStringMapKeys(n.ConfigSources) {
		source := n.ConfigSources[key]
		if !graph.KnownConfigSource(source) {
			report.ownershipFailure("%s: configSources[%q] has unknown origin %q", prefix, key, source)
		}
		if _, ok := n.Config[key]; !ok {
			report.ownershipFailure("%s: configSources[%q] names a key the node does not store", prefix, key)
		}
	}
}

func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

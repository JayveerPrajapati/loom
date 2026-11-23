package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"loom/internal/graph"
)

// CredentialProfileUse is one current profile whose credentialRefs include
// the credential. Slots are the profile slots that point at it.
type CredentialProfileUse struct {
	ProfileID  string   `json:"profileId"`
	Name       string   `json:"name"`
	NodeType   string   `json:"nodeType"`
	Version    int      `json:"version"`
	Visibility string   `json:"visibility,omitempty"`
	CreatorID  string   `json:"creatorId,omitempty"`
	Slots      []string `json:"slots"`
}

// CredentialWorkflowUse is one published workflow version, or the current
// workflow document when Current is true, whose nodes reference the
// credential.
type CredentialWorkflowUse struct {
	WorkflowID string   `json:"workflowId"`
	Name       string   `json:"name"`
	Version    int      `json:"version"`
	Current    bool     `json:"current,omitempty"`
	Slots      []string `json:"slots"`
}

// CredentialUsage lists every profile and workflow reference to a
// credential. It is computed from the current profile docs and the stored
// workflow docs instead of a usage index: credential administration is
// admin-only and low-volume, and a scan cannot drift from the documents.
type CredentialUsage struct {
	CredentialID string                  `json:"credentialId"`
	Profiles     []CredentialProfileUse  `json:"profiles"`
	Workflows    []CredentialWorkflowUse `json:"workflows"`
}

// CredentialUsage returns the profiles and workflow documents that
// reference the credential.
func (s *Store) CredentialUsage(credentialID string) (CredentialUsage, error) {
	usage := CredentialUsage{
		CredentialID: credentialID,
		Profiles:     []CredentialProfileUse{},
		Workflows:    []CredentialWorkflowUse{},
	}
	seenProfiles := map[string]bool{}
	seenWorkflows := map[string]bool{}
	err := s.scanCredentialRefs(func(id string, profile *CredentialProfileUse, workflow *CredentialWorkflowUse) error {
		if id != credentialID {
			return nil
		}
		if profile != nil && !seenProfiles[profile.ProfileID] {
			seenProfiles[profile.ProfileID] = true
			usage.Profiles = append(usage.Profiles, *profile)
		}
		if workflow != nil {
			key := workflow.WorkflowID + ":" + strconv.Itoa(workflow.Version) + ":" + strconv.FormatBool(workflow.Current)
			if !seenWorkflows[key] {
				seenWorkflows[key] = true
				usage.Workflows = append(usage.Workflows, *workflow)
			}
		}
		return nil
	})
	if err != nil {
		return CredentialUsage{}, err
	}
	sort.Slice(usage.Profiles, func(i, j int) bool {
		if usage.Profiles[i].Name != usage.Profiles[j].Name {
			return usage.Profiles[i].Name < usage.Profiles[j].Name
		}
		return usage.Profiles[i].ProfileID < usage.Profiles[j].ProfileID
	})
	sort.Slice(usage.Workflows, func(i, j int) bool {
		if usage.Workflows[i].Name != usage.Workflows[j].Name {
			return usage.Workflows[i].Name < usage.Workflows[j].Name
		}
		if usage.Workflows[i].WorkflowID != usage.Workflows[j].WorkflowID {
			return usage.Workflows[i].WorkflowID < usage.Workflows[j].WorkflowID
		}
		if usage.Workflows[i].Version != usage.Workflows[j].Version {
			return usage.Workflows[i].Version < usage.Workflows[j].Version
		}
		return !usage.Workflows[i].Current && usage.Workflows[j].Current
	})
	return usage, nil
}

// CredentialUsageCounts counts distinct profile and workflow references for
// every credential in one scan. The credential list page uses it so a row
// can show its reference count without a query per row.
func (s *Store) CredentialUsageCounts() (map[string]int, error) {
	sets := map[string]map[string]bool{}
	err := s.scanCredentialRefs(func(id string, profile *CredentialProfileUse, workflow *CredentialWorkflowUse) error {
		if sets[id] == nil {
			sets[id] = map[string]bool{}
		}
		if profile != nil {
			sets[id]["profile:"+profile.ProfileID] = true
		}
		if workflow != nil {
			sets[id]["workflow:"+workflow.WorkflowID+":"+strconv.Itoa(workflow.Version)+":"+strconv.FormatBool(workflow.Current)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(sets))
	for id, refs := range sets {
		counts[id] = len(refs)
	}
	return counts, nil
}

func (s *Store) scanCredentialRefs(fn func(credentialID string, profile *CredentialProfileUse, workflow *CredentialWorkflowUse) error) error {
	if err := s.scanProfileCredentialRefs(fn); err != nil {
		return err
	}
	if err := s.scanWorkflowCredentialRefs(fn); err != nil {
		return err
	}
	return nil
}

func (s *Store) scanProfileCredentialRefs(fn func(string, *CredentialProfileUse, *CredentialWorkflowUse) error) error {
	rows, err := s.read(`SELECT p.id, p.name, p.node_type, p.current_version, p.visibility, p.creator_id, v.doc
		FROM config_profiles p
		JOIN config_profile_versions v ON v.profile_id = p.id AND v.version = p.current_version
		ORDER BY lower(p.name), p.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var use CredentialProfileUse
		var doc []byte
		if err := rows.Scan(&use.ProfileID, &use.Name, &use.NodeType, &use.Version, &use.Visibility, &use.CreatorID, &doc); err != nil {
			return err
		}
		var stored configProfileDoc
		if err := json.Unmarshal(doc, &stored); err != nil {
			return fmt.Errorf("read profile %s credential refs: %w", use.ProfileID, err)
		}
		for credentialID, slots := range credentialSlots(stored.CredentialRefs) {
			item := use
			item.Slots = slots
			if err := fn(credentialID, &item, nil); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func (s *Store) scanWorkflowCredentialRefs(fn func(string, *CredentialProfileUse, *CredentialWorkflowUse) error) error {
	versionRows, err := s.read(`SELECT w.id, w.name, v.version, v.doc
		FROM workflow_versions v
		JOIN workflows w ON w.id = v.workflow_id
		ORDER BY lower(w.name), w.id, v.version`)
	if err != nil {
		return err
	}
	defer versionRows.Close()
	for versionRows.Next() {
		var use CredentialWorkflowUse
		var doc []byte
		if err := versionRows.Scan(&use.WorkflowID, &use.Name, &use.Version, &doc); err != nil {
			return err
		}
		if err := emitWorkflowCredentialRefs(doc, use, fn); err != nil {
			return err
		}
	}
	if err := versionRows.Err(); err != nil {
		return err
	}
	currentRows, err := s.read(`SELECT w.id, w.name, w.published_version, w.doc, COALESCE(v.content_hash,'')
		FROM workflows w
		LEFT JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.published_version
		ORDER BY lower(w.name), w.id`)
	if err != nil {
		return err
	}
	defer currentRows.Close()
	for currentRows.Next() {
		var use CredentialWorkflowUse
		var doc []byte
		var publishedHash string
		if err := currentRows.Scan(&use.WorkflowID, &use.Name, &use.Version, &doc, &publishedHash); err != nil {
			return err
		}
		// The current doc is the published snapshot after a publish; the
		// version row already covers those references. Only a draft that
		// differs from its published version adds a current row.
		if use.Version > 0 && publishedHash != "" && publishedHash == contentHash(doc) {
			continue
		}
		use.Current = true
		if err := emitWorkflowCredentialRefs(doc, use, fn); err != nil {
			return err
		}
	}
	return currentRows.Err()
}

func emitWorkflowCredentialRefs(doc []byte, use CredentialWorkflowUse, fn func(string, *CredentialProfileUse, *CredentialWorkflowUse) error) error {
	var wf graph.Workflow
	if err := json.Unmarshal(doc, &wf); err != nil {
		return fmt.Errorf("read workflow %s credential refs: %w", use.WorkflowID, err)
	}
	byCredential := map[string][]string{}
	for _, node := range wf.Nodes {
		for slot, credentialID := range node.Credentials {
			if credentialID == "" {
				continue
			}
			byCredential[credentialID] = append(byCredential[credentialID], slot)
		}
	}
	for credentialID, slots := range byCredential {
		sort.Strings(slots)
		item := use
		item.Slots = slots
		if err := fn(credentialID, nil, &item); err != nil {
			return err
		}
	}
	return nil
}

func credentialSlots(refs map[string]string) map[string][]string {
	out := map[string][]string{}
	for slot, credentialID := range refs {
		if credentialID == "" {
			continue
		}
		out[credentialID] = append(out[credentialID], slot)
	}
	for credentialID := range out {
		sort.Strings(out[credentialID])
	}
	return out
}

// CredentialExists reports whether the id is stored.
func (s *Store) CredentialExists(id string) (bool, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM credentials WHERE id = ?`, id).Scan(&count)
	return count > 0, err
}

// GetCredentialInfo returns the non-secret metadata for one credential.
func (s *Store) GetCredentialInfo(id string) (CredentialInfo, error) {
	var item CredentialInfo
	var created string
	err := s.readRow(`SELECT id, type, created_at FROM credentials WHERE id = ?`, id).
		Scan(&item.ID, &item.Type, &created)
	if err == sql.ErrNoRows {
		return CredentialInfo{}, fmt.Errorf("credential %q not found", id)
	}
	if err != nil {
		return CredentialInfo{}, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return item, nil
}

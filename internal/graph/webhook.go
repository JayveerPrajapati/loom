package graph

import (
	"fmt"
	"strings"
)

// WebhookPaths maps each /hook path in wf to the node that declares it.
func WebhookPaths(wf *Workflow) map[string]string {
	paths := map[string]string{}
	if wf == nil {
		return paths
	}
	for _, node := range wf.Nodes {
		if node.Type != "trigger.webhook" {
			continue
		}
		path, _ := node.Config["path"].(string)
		path = strings.Trim(path, "/")
		if path == "" {
			continue
		}
		paths[path] = node.ID
	}
	return paths
}

// ValidateWebhookPaths rejects a webhook path that is already owned by another
// workflow. Shared by the API and `loom import` so both enforce the same rule;
// otherwise the first workflow in listing order silently shadows the other.
func ValidateWebhookPaths(wf *Workflow, others []*Workflow) error {
	paths := WebhookPaths(wf)
	if len(paths) == 0 {
		return nil
	}
	for _, other := range others {
		if other == nil || other.ID == wf.ID {
			continue
		}
		for _, node := range other.Nodes {
			if node.Type != "trigger.webhook" {
				continue
			}
			path, _ := node.Config["path"].(string)
			path = strings.Trim(path, "/")
			if path == "" {
				continue
			}
			if _, ok := paths[path]; ok {
				return fmt.Errorf("webhook path %q is already used by %q", "/hook/"+path, other.Name)
			}
		}
	}
	return nil
}

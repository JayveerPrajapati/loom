package graph

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"loom/internal/nodes"
)

var nodeRefRe = regexp.MustCompile(`\$node\s*\[\s*["']([^"']+)["']\s*\]`)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

type Issue struct {
	Severity Severity
	Code     string
	Node     string
	Edge     *Edge
	Message  string
}

func (i Issue) String() string {
	loc := ""
	if i.Node != "" {
		loc = fmt.Sprintf("node %q", i.Node)
	}
	if i.Edge != nil {
		loc = fmt.Sprintf("edge %s.%s -> %s.%s", i.Edge.From.Node, i.Edge.From.Port, i.Edge.To.Node, i.Edge.To.Port)
	}
	if loc == "" {
		return fmt.Sprintf("[%s] %s: %s", i.Severity, i.Code, i.Message)
	}
	return fmt.Sprintf("[%s] %s: %s: %s", i.Severity, i.Code, loc, i.Message)
}

type Report struct {
	Issues []Issue
}

func (r Report) HasErrors() bool {
	for _, i := range r.Issues {
		if i.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (r Report) HasIssues() bool { return len(r.Issues) > 0 }

func (r *Report) errorf(code, node, msg string, e *Edge) {
	r.Issues = append(r.Issues, Issue{Severity: SeverityError, Code: code, Node: node, Edge: e, Message: msg})
}

func (r *Report) warnf(code, node, msg string, e *Edge) {
	r.Issues = append(r.Issues, Issue{Severity: SeverityWarning, Code: code, Node: node, Edge: e, Message: msg})
}

// Compatible reports whether a value produced as src may flow into dst.
// An untyped json producer narrows with a warning; widening to json is safe.
func Compatible(src, dst nodes.PortType) (ok bool, warn bool) {
	if src == nodes.TypeAny || dst == nodes.TypeAny {
		return true, false
	}
	if src == dst {
		return true, false
	}
	if src == nodes.TypeJSON {
		return true, true
	}
	if dst == nodes.TypeJSON {
		return true, false
	}
	return false, false
}

// Validate checks structure, contracts, ports, credentials and acyclicity.
func Validate(wf *Workflow) Report {
	var r Report
	if wf == nil {
		r.errorf("nil_workflow", "", "workflow is nil", nil)
		return r
	}
	if strings.TrimSpace(wf.ID) == "" {
		r.errorf("missing_id", "", "workflow id is required", nil)
	}
	if strings.TrimSpace(wf.Name) == "" {
		r.errorf("missing_name", "", "workflow name is required", nil)
	}
	if wf.Version < 1 {
		r.errorf("invalid_version", "", "workflow version must be >= 1", nil)
	}
	if len(wf.Nodes) == 0 {
		r.errorf("no_nodes", "", "workflow must contain at least one node", nil)
		return r
	}

	byID := make(map[string]*Node, len(wf.Nodes))
	defs := make(map[string]nodes.Definition, len(wf.Nodes))
	for idx := range wf.Nodes {
		n := &wf.Nodes[idx]
		if strings.TrimSpace(n.ID) == "" {
			r.errorf("missing_node_id", "", "every node needs an id", nil)
			continue
		}
		if _, dup := byID[n.ID]; dup {
			r.errorf("duplicate_node", n.ID, "node id is used more than once", nil)
			continue
		}
		byID[n.ID] = n

		def, ok := nodes.Get(n.Type)
		if !ok {
			r.errorf("unknown_node_type", n.ID, fmt.Sprintf("unknown node type %q", n.Type), nil)
			continue
		}
		defs[n.ID] = def
		if n.TypeVersion != def.TypeVersion {
			r.errorf("unsupported_type_version", n.ID,
				fmt.Sprintf("%s supports typeVersion %d, got %d", def.Type, def.TypeVersion, n.TypeVersion), nil)
		}
		if strings.TrimSpace(n.Name) == "" {
			r.warnf("missing_node_name", n.ID, "node has no display name", nil)
		}
		validateConfig(&r, n, def)
		validateCredentials(&r, n, def)
		validateRetry(&r, n)
		validateOnError(&r, n)
		validateBatch(&r, n)
	}

	// Edges: endpoints, ports, single writer per input, contract compatibility.
	incoming := make(map[string]int, len(wf.Edges))
	for idx := range wf.Edges {
		e := &wf.Edges[idx]
		src, srcOK := byID[e.From.Node]
		_, dstOK := byID[e.To.Node]
		if !srcOK {
			r.errorf("unknown_node", "", fmt.Sprintf("edge source %q does not exist", e.From.Node), e)
			continue
		}
		if !dstOK {
			r.errorf("unknown_node", "", fmt.Sprintf("edge target %q does not exist", e.To.Node), e)
			continue
		}
		if e.From.Node == e.To.Node {
			r.errorf("self_edge", e.From.Node, "node cannot connect to itself", e)
			continue
		}

		srcDef, srcKnown := defs[e.From.Node]
		dstDef, dstKnown := defs[e.To.Node]
		if !srcKnown || !dstKnown {
			continue
		}

		if !contains(srcDef.Outputs, e.From.Port) {
			if !(e.From.Port == "error" && src.OnError == "branch") {
				r.errorf("unknown_port", e.From.Node,
					fmt.Sprintf("%s has no output port %q (have %s)", srcDef.Type, e.From.Port, strings.Join(srcDef.Outputs, ", ")), e)
				continue
			}
		}
		if !contains(dstDef.Inputs, e.To.Port) {
			r.errorf("unknown_port", e.To.Node,
				fmt.Sprintf("%s has no input port %q (have %s)", dstDef.Type, e.To.Port, strings.Join(dstDef.Inputs, ", ")), e)
			continue
		}

		key := e.To.Node + "\x00" + e.To.Port
		incoming[key]++
		if incoming[key] > 1 {
			r.errorf("duplicate_input", e.To.Node,
				fmt.Sprintf("input port %q already has a connection; use a merge node", e.To.Port), e)
		}

		if dstDef.Category == "trigger" {
			r.errorf("trigger_input", e.To.Node, "trigger nodes cannot have incoming edges", e)
			continue
		}

		srcType := srcDef.OutputTypeFor(src.Config)
		if e.From.Port == "error" {
			srcType = "json"
		}
		if ok, warn := Compatible(srcType, dstDef.InputType); !ok {
			r.errorf("type_mismatch", e.To.Node,
				fmt.Sprintf("%s produces %q but %s expects %q", srcDef.Type, srcType, dstDef.Type, dstDef.InputType), e)
		} else if warn {
			r.warnf("untyped_edge", "", fmt.Sprintf("%q flows into %q; add a typed producer", srcType, dstDef.InputType), e)
		}
	}

	triggers := make([]*Node, 0, 1)
	for idx := range wf.Nodes {
		n := &wf.Nodes[idx]
		if def, ok := nodes.Get(n.Type); ok && def.Category == "trigger" {
			triggers = append(triggers, n)
		}
	}
	switch {
	case len(triggers) == 0:
		r.errorf("no_trigger", "", "workflow needs exactly one trigger node", nil)
	case len(triggers) > 1:
		ids := make([]string, 0, len(triggers))
		for _, t := range triggers {
			ids = append(ids, t.ID)
		}
		sort.Strings(ids)
		r.errorf("multiple_triggers", "", "workflow needs exactly one trigger node, found: "+strings.Join(ids, ", "), nil)
	}

	if cycle := findCycle(wf, byID); len(cycle) > 0 {
		r.errorf("cycle", "", "workflow graph must be a DAG; cycle: "+strings.Join(cycle, " -> "), nil)
	}

	if len(triggers) > 0 {
		reach := reachable(wf, byID, triggers)
		ids := make([]string, 0, len(wf.Nodes))
		for _, n := range wf.Nodes {
			if !reach[n.ID] {
				ids = append(ids, n.ID)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			r.warnf("unreachable", id, "node is not reachable from a trigger", nil)
		}
	}

	validateNodeRefs(&r, wf, byID)
	return r
}

func validateNodeRefs(r *Report, wf *Workflow, byID map[string]*Node) {
	ancestors := make(map[string]map[string]bool, len(wf.Nodes))
	for _, node := range wf.Nodes {
		ancestors[node.ID] = ancestorSet(wf, node.ID)
	}
	for _, node := range wf.Nodes {
		refs := map[string]bool{}
		collectNodeRefs(node.Config, refs)
		ids := make([]string, 0, len(refs))
		for id := range refs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, ref := range ids {
			if ref == node.ID {
				r.errorf("node_self_reference", node.ID, fmt.Sprintf("node %q cannot reference its own output", ref), nil)
				continue
			}
			if _, ok := byID[ref]; !ok {
				r.errorf("unknown_node_reference", node.ID, fmt.Sprintf("expression references unknown node %q", ref), nil)
				continue
			}
			if !ancestors[node.ID][ref] {
				r.errorf("node_reference_not_upstream", node.ID,
					fmt.Sprintf("expression references %q, which is not upstream of this node", ref), nil)
			}
		}
	}
}

func collectNodeRefs(value any, refs map[string]bool) {
	switch v := value.(type) {
	case string:
		for _, match := range nodeRefRe.FindAllStringSubmatch(v, -1) {
			if len(match) == 2 {
				refs[match[1]] = true
			}
		}
	case map[string]any:
		for _, inner := range v {
			collectNodeRefs(inner, refs)
		}
	case []any:
		for _, inner := range v {
			collectNodeRefs(inner, refs)
		}
	}
}

func ancestorSet(wf *Workflow, nodeID string) map[string]bool {
	parents := make(map[string][]string, len(wf.Edges))
	for _, edge := range wf.Edges {
		parents[edge.To.Node] = append(parents[edge.To.Node], edge.From.Node)
	}
	seen := map[string]bool{}
	stack := append([]string{}, parents[nodeID]...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, parents[id]...)
	}
	return seen
}

func validateBatch(r *Report, n *Node) {
	if n.Batch == nil {
		return
	}
	path := strings.TrimSpace(n.Batch.ItemsPath)
	if path == "" {
		path = "items"
	}
	if strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") || strings.Contains(path, "..") {
		r.errorf("invalid_batch_path", n.ID, "batch.itemsPath must be a dotted object path", nil)
	}
}

func validateOnError(r *Report, n *Node) {
	switch n.OnError {
	case "", "stop", "continue", "branch":
	default:
		r.errorf("invalid_on_error", n.ID, `onError must be "stop", "continue" or "branch"`, nil)
	}
}

func validateRetry(r *Report, n *Node) {
	if n.Retry == nil {
		return
	}
	if n.Retry.MaxAttempts < 0 || n.Retry.MaxAttempts > 20 {
		r.errorf("invalid_retry_attempts", n.ID, "retry.maxAttempts must be 0 (engine default) or 1-20", nil)
	}
	if n.Retry.BackoffMs < 0 || n.Retry.BackoffMs > 600000 {
		r.errorf("invalid_retry_backoff", n.ID, "retry.backoffMs must be 0-600000", nil)
	}
	if n.Retry.MaxDelayMs < 0 || n.Retry.MaxDelayMs > 3600000 {
		r.errorf("invalid_retry_max_delay", n.ID, "retry.maxDelayMs must be 0-3600000", nil)
	}
	if n.Retry.BackoffMs > 0 && n.Retry.MaxDelayMs > 0 && n.Retry.BackoffMs > n.Retry.MaxDelayMs {
		r.errorf("retry_backoff_exceeds_max", n.ID, "retry.backoffMs must not exceed retry.maxDelayMs", nil)
	}
}

func validateConfig(r *Report, n *Node, def nodes.Definition) {
	defKeys := make([]string, 0, len(def.Config))
	for key := range def.Config {
		defKeys = append(defKeys, key)
	}
	sort.Strings(defKeys)

	for _, key := range defKeys {
		spec := def.Config[key]
		raw, present := n.Config[key]
		if !present {
			if spec.Required {
				r.errorf("missing_config", n.ID, fmt.Sprintf("%s requires config %q", def.Type, key), nil)
			}
			continue
		}
		if !fieldMatches(raw, spec.Type) {
			r.errorf("invalid_config", n.ID, fmt.Sprintf("config %q must be a %s", key, spec.Type), nil)
			continue
		}
		if len(spec.Enum) > 0 {
			s, _ := raw.(string)
			if !contains(spec.Enum, s) {
				r.errorf("invalid_config", n.ID,
					fmt.Sprintf("config %q must be one of %s, got %q", key, strings.Join(spec.Enum, ", "), s), nil)
			}
		}
	}
	if n.Config == nil {
		return
	}
	configKeys := make([]string, 0, len(n.Config))
	for key := range n.Config {
		configKeys = append(configKeys, key)
	}
	sort.Strings(configKeys)
	for _, key := range configKeys {
		if _, ok := def.Config[key]; !ok {
			r.errorf("unknown_config", n.ID, fmt.Sprintf("%s has no config field %q", def.Type, key), nil)
		}
	}
}

func validateCredentials(r *Report, n *Node, def nodes.Definition) {
	for _, cred := range def.Credentials {
		if contains(def.OptionalCredentials, cred) {
			continue
		}
		if strings.TrimSpace(n.Credentials[cred]) == "" {
			r.errorf("missing_credential", n.ID, fmt.Sprintf("%s requires a %q credential", def.Type, cred), nil)
		}
	}
	for key := range n.Credentials {
		if !contains(def.Credentials, key) && !contains(def.OptionalCredentials, key) {
			r.warnf("unknown_credential", n.ID, fmt.Sprintf("%s does not use credential %q", def.Type, key), nil)
		}
	}
}

func fieldMatches(v any, t nodes.FieldType) bool {
	switch t {
	case nodes.FieldString:
		_, ok := v.(string)
		return ok
	case nodes.FieldNumber:
		_, ok := v.(float64)
		return ok
	case nodes.FieldBoolean:
		_, ok := v.(bool)
		return ok
	case nodes.FieldArray:
		_, ok := v.([]any)
		return ok
	case nodes.FieldObject:
		_, ok := v.(map[string]any)
		return ok
	default:
		return false
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func findCycle(wf *Workflow, byID map[string]*Node) []string {
	adj := make(map[string][]string, len(wf.Nodes))
	for _, e := range wf.Edges {
		if _, ok := byID[e.From.Node]; !ok {
			continue
		}
		if _, ok := byID[e.To.Node]; !ok {
			continue
		}
		adj[e.From.Node] = append(adj[e.From.Node], e.To.Node)
	}

	color := make(map[string]int, len(byID)) // 0 unvisited, 1 in stack, 2 done
	stack := make([]string, 0, len(byID))

	var visit func(string) []string
	visit = func(u string) []string {
		color[u] = 1
		stack = append(stack, u)
		for _, v := range adj[u] {
			switch color[v] {
			case 1:
				start := 0
				for i, s := range stack {
					if s == v {
						start = i
						break
					}
				}
				cyc := append([]string{}, stack[start:]...)
				return append(cyc, v)
			case 0:
				if c := visit(v); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = 2
		return nil
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == 0 {
			if c := visit(id); c != nil {
				return c
			}
		}
	}
	return nil
}

func reachable(wf *Workflow, byID map[string]*Node, triggers []*Node) map[string]bool {
	adj := make(map[string][]string, len(wf.Nodes))
	for _, e := range wf.Edges {
		if _, ok := byID[e.From.Node]; !ok {
			continue
		}
		if _, ok := byID[e.To.Node]; !ok {
			continue
		}
		adj[e.From.Node] = append(adj[e.From.Node], e.To.Node)
	}

	seen := make(map[string]bool, len(wf.Nodes))
	stack := make([]string, 0, len(triggers))
	for _, t := range triggers {
		stack = append(stack, t.ID)
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[u] {
			continue
		}
		seen[u] = true
		stack = append(stack, adj[u]...)
	}
	return seen
}

// Plan is a topological layering of the DAG: nodes in one level may run in parallel.
type Plan struct {
	Levels [][]string
}

func BuildPlan(wf *Workflow) (Plan, error) {
	byID := make(map[string]struct{}, len(wf.Nodes))
	for _, n := range wf.Nodes {
		byID[n.ID] = struct{}{}
	}

	indeg := make(map[string]int, len(wf.Nodes))
	adj := make(map[string][]string, len(wf.Nodes))
	for _, n := range wf.Nodes {
		indeg[n.ID] = 0
	}
	for _, e := range wf.Edges {
		if _, ok := byID[e.From.Node]; !ok {
			return Plan{}, fmt.Errorf("edge source %q does not exist", e.From.Node)
		}
		if _, ok := byID[e.To.Node]; !ok {
			return Plan{}, fmt.Errorf("edge target %q does not exist", e.To.Node)
		}
		adj[e.From.Node] = append(adj[e.From.Node], e.To.Node)
		indeg[e.To.Node]++
	}

	level := make([]string, 0, len(wf.Nodes))
	for id, d := range indeg {
		if d == 0 {
			level = append(level, id)
		}
	}
	sort.Strings(level)

	var levels [][]string
	seen := 0
	for len(level) > 0 {
		levels = append(levels, level)
		seen += len(level)
		next := make([]string, 0, len(level))
		for _, u := range level {
			for _, v := range adj[u] {
				indeg[v]--
				if indeg[v] == 0 {
					next = append(next, v)
				}
			}
		}
		sort.Strings(next)
		level = next
	}

	if seen != len(wf.Nodes) {
		return Plan{}, fmt.Errorf("workflow contains a cycle")
	}
	return Plan{Levels: levels}, nil
}

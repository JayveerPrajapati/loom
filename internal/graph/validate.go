package graph

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"loom/internal/egress"
	"loom/internal/nodes"
	"loom/internal/secrets"
)

var nodeRefRe = regexp.MustCompile(`\$node\s*\[\s*["']([^"']+)["']\s*\]`)

const (
	// maxNodes and maxEdges cap a single workflow. They are far above any
	// hand-authored graph but low enough that validation stays bounded.
	maxNodes = 1000
	maxEdges = 4000
	// maxBatchConcurrency bounds the fan-out of one batch node. The engine
	// builds a semaphore straight from this value, so an unbounded config
	// would let one request open thousands of outbound calls.
	maxBatchConcurrency = 32
	// maxSwitchCases matches the case0..case2 output ports of flow.switch.
	maxSwitchCases = 3
)

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

// Validate checks structure, contracts, ports, credentials and acyclicity. It
// also enforces the ingest size caps.
func Validate(wf *Workflow) Report {
	return validate(wf, true, nil)
}

// ValidateStored checks an already-stored workflow. Size caps are enforced at
// ingest only, so a workflow saved before the caps existed can still run.
func ValidateStored(wf *Workflow) Report {
	return validate(wf, false, nil)
}

// ExtraDefs resolves connector-authored node definitions (operational-
// trust Campaign 4): the server and engine merge them over the built-in
// catalog so connector.* nodes validate with the same machinery.
type ExtraDefs func(nodeType string) (nodes.Definition, bool)

// ValidateWithExtra is Validate plus connector definitions.
func ValidateWithExtra(wf *Workflow, extra ExtraDefs) Report {
	return validate(wf, true, extra)
}

// ValidateStoredWithExtra is ValidateStored plus connector definitions.
func ValidateStoredWithExtra(wf *Workflow, extra ExtraDefs) Report {
	return validate(wf, false, extra)
}

func validate(wf *Workflow, enforceSize bool, extra ExtraDefs) Report {
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
	// The workflow's own egress allowlist is validated with the same
	// entry rules as the global policy, so a bad entry is caught at
	// save/publish rather than becoming a runtime denial surprise.
	if wf.Egress != nil {
		for _, entry := range wf.Egress.Allowlist {
			if err := egress.ValidateEntry(entry); err != nil {
				r.errorf("invalid_egress", "", "workflow egress: "+err.Error(), nil)
			}
		}
	}
	if len(wf.Nodes) == 0 {
		r.errorf("no_nodes", "", "workflow must contain at least one node", nil)
		return r
	}
	// Bound the work one authenticated request can force. Validation is at
	// least O(V+E) and reference checking is quadratic in the worst case, so a
	// 5 MiB body must not translate into minutes of pinned CPU.
	if enforceSize && len(wf.Nodes) > maxNodes {
		r.errorf("workflow_too_large", "", fmt.Sprintf("workflow has %d nodes; the maximum is %d", len(wf.Nodes), maxNodes), nil)
		return r
	}
	if enforceSize && len(wf.Edges) > maxEdges {
		r.errorf("workflow_too_large", "", fmt.Sprintf("workflow has %d edges; the maximum is %d", len(wf.Edges), maxEdges), nil)
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
		if !ok && extra != nil {
			def, ok = extra(n.Type)
		}
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
		validateConfig(&r, n, def, enforceSize)
		validateCredentials(&r, n, def)
		validateRetry(&r, n)
		validateOnError(&r, n)
		validateBatch(&r, n, enforceSize)
		if n.Type == "http.request" {
			validateHTTP(&r, n)
		}
		if n.Type == "code.python" {
			validatePython(&r, n)
		}
		if n.Type == "code.run" || n.Type == "code.python" {
			validateCode(&r, n)
		}
		if enforceSize {
			validateSwitch(&r, n)
		}
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

	// A webhook path must have one owner inside one workflow too; otherwise
	// the second trigger is silently unreachable at dispatch time.
	webhookPaths := map[string]string{}
	for idx := range wf.Nodes {
		n := &wf.Nodes[idx]
		if n.Type != "trigger.webhook" {
			continue
		}
		path, _ := n.Config["path"].(string)
		path = strings.Trim(path, "/")
		if path == "" {
			continue
		}
		if prev, ok := webhookPaths[path]; ok {
			r.errorf("duplicate_webhook_path", n.ID, fmt.Sprintf("webhook path %q is also used by node %q", "/hook/"+path, prev), nil)
			continue
		}
		webhookPaths[path] = n.ID
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
	ancestors := buildAncestorIndex(wf)
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
			if !ancestors.isAncestor(node.ID, ref) {
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

// ancestorIndex answers "is X an ancestor of Y" in O(1) after one topological
// pass. Ancestor sets are stored as bitsets, so the whole index costs
// O(V^2/64) words instead of rebuilding a parents map per node reference.
type ancestorIndex struct {
	order map[string]int
	sets  [][]uint64
}

func (a ancestorIndex) isAncestor(node, ref string) bool {
	ni, ok := a.order[node]
	if !ok {
		return false
	}
	ri, ok := a.order[ref]
	if !ok {
		return false
	}
	if ni >= len(a.sets) || a.sets[ni] == nil {
		return false
	}
	return a.sets[ni][ri/64]&(uint64(1)<<uint(ri%64)) != 0
}

func buildAncestorIndex(wf *Workflow) ancestorIndex {
	n := len(wf.Nodes)
	words := (n + 63) / 64
	order := make(map[string]int, n)
	for i := range wf.Nodes {
		if _, dup := order[wf.Nodes[i].ID]; !dup && strings.TrimSpace(wf.Nodes[i].ID) != "" {
			order[wf.Nodes[i].ID] = i
		}
	}
	parents := make([][]int, n)
	children := make([][]int, n)
	indeg := make([]int, n)
	for _, e := range wf.Edges {
		pi, ok1 := order[e.From.Node]
		ci, ok2 := order[e.To.Node]
		if !ok1 || !ok2 {
			continue
		}
		parents[ci] = append(parents[ci], pi)
		children[pi] = append(children[pi], ci)
		indeg[ci]++
	}
	sets := make([][]uint64, n)
	queue := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		ci := queue[0]
		queue = queue[1:]
		set := make([]uint64, words)
		for _, pi := range parents[ci] {
			set[pi/64] |= uint64(1) << uint(pi%64)
			for w := 0; w < words; w++ {
				set[w] |= sets[pi][w]
			}
		}
		sets[ci] = set
		for _, child := range children[ci] {
			indeg[child]--
			if indeg[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	return ancestorIndex{order: order, sets: sets}
}

func validateBatch(r *Report, n *Node, enforceSize bool) {
	if n.Batch == nil {
		return
	}
	if enforceSize && (n.Batch.Concurrency < 0 || n.Batch.Concurrency > maxBatchConcurrency) {
		r.errorf("invalid_batch_concurrency", n.ID,
			fmt.Sprintf("batch.concurrency must be 0 (engine default) or 1-%d, got %d", maxBatchConcurrency, n.Batch.Concurrency), nil)
	}
	path := strings.TrimSpace(n.Batch.ItemsPath)
	if path == "" {
		path = "items"
	}
	if strings.HasPrefix(path, ".") || strings.HasSuffix(path, ".") || strings.Contains(path, "..") {
		r.errorf("invalid_batch_path", n.ID, "batch.itemsPath must be a dotted object path", nil)
	}
}

// validateSwitch rejects more cases than the node declares output ports for.
// The runtime maps case index >2 to the default port, so extra cases would be
// silently dropped.
func validateSwitch(r *Report, n *Node) {
	if n.Type != "flow.switch" {
		return
	}
	cases, ok := n.Config["cases"].([]any)
	if !ok {
		return // validateConfig already reported the type mismatch.
	}
	if len(cases) > maxSwitchCases {
		r.errorf("too_many_switch_cases", n.ID,
			fmt.Sprintf("flow.switch supports at most %d cases (case0..case%d), got %d", maxSwitchCases, maxSwitchCases-1, len(cases)), nil)
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

// validateHTTP guards the http.request pagination config at save/publish
// time: loop shape errors must be caught before a run ever starts, not
// surface as a mid-run failure after N pages.
func validateHTTP(r *Report, n *Node) {
	// OAuth2 needs its credential declared before a run can ever start.
	auth := strings.ToLower(strings.TrimSpace(configString(n.Config["auth"])))
	if auth == "oauth2" && strings.TrimSpace(n.Credentials["oauth2"]) == "" {
		r.errorf("missing_oauth2_credential", n.ID,
			`auth "oauth2" requires an "oauth2" credential (tokenUrl, clientId, clientSecret, scopes)`, nil)
	}
	// GraphQL bodies are validated like every other templated field.
	if strings.ToLower(strings.TrimSpace(configString(n.Config["bodyType"]))) == "graphql" &&
		strings.TrimSpace(configString(n.Config["graphqlQuery"])) == "" {
		r.errorf("missing_graphql_query", n.ID, "bodyType graphql requires graphqlQuery", nil)
	}
	// Parameter-group rows (v3 plan D2/D3): row-shape errors are caught
	// at draft save, before publish ever compiles them.
	validateHTTPParamRows(r, n)
	pag, ok := n.Config["pagination"].(map[string]any)
	if !ok {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(configString(pag["mode"])))
	switch mode {
	case "page", "offset", "cursor":
	case "":
		r.errorf("invalid_pagination_mode", n.ID, "pagination.mode is required (page, offset or cursor)", nil)
		return
	default:
		r.errorf("invalid_pagination_mode", n.ID, fmt.Sprintf("pagination.mode must be page, offset or cursor, got %q", mode), nil)
		return
	}
	if v := configNumber(pag["maxPages"]); v < 0 || v > 1000 {
		r.errorf("invalid_pagination_max_pages", n.ID, "pagination.maxPages must be 1-1000", nil)
	}
	if v := configNumber(pag["maxItems"]); v < 0 || v > 100000 {
		r.errorf("invalid_pagination_max_items", n.ID, "pagination.maxItems must be 1-100000", nil)
	}
	if mode == "offset" && configNumber(pag["limit"]) <= 0 {
		r.errorf("invalid_pagination_limit", n.ID, "pagination mode offset requires a positive limit (the offset step)", nil)
	}
	if mode == "cursor" && strings.TrimSpace(configString(pag["cursorPath"])) == "" {
		r.errorf("invalid_pagination_cursor_path", n.ID, "pagination mode cursor requires cursorPath (where the next-page cursor lives in the response)", nil)
	}
}

// validateHTTPParamRows validates the typed parameter-row editors and the
// per-group mapping refs (Configuration Hub v3 plan §5.2) at draft save
// and publish: every row must parse and every mappingRefs key must be a
// known group with a non-empty id. Dangling refs and kind mismatches are
// store-level checks — they need the mapping table.
func validateHTTPParamRows(r *Report, n *Node) {
	for _, fieldsKey := range []string{"bodyFields", "queryFields", "headerFields", "pathFields"} {
		raw, present := n.Config[fieldsKey]
		if !present {
			continue
		}
		if _, err := ParseParamRows(raw); err != nil {
			r.errorf("invalid_param_rows", n.ID, fmt.Sprintf("%s %s", fieldsKey, err.Error()), nil)
		}
	}
	refs, ok := n.Config["mappingRefs"].(map[string]any)
	if !ok {
		return
	}
	for group, value := range refs {
		switch group {
		case "body", "query", "headers", "pathParams":
		default:
			r.errorf("invalid_mapping_refs", n.ID,
				fmt.Sprintf("mappingRefs has unknown group %q (want body, query, headers or pathParams)", group), nil)
			continue
		}
		id, _ := value.(string)
		if strings.TrimSpace(id) == "" {
			r.errorf("invalid_mapping_refs", n.ID,
				fmt.Sprintf("mappingRefs.%s must be a non-empty mapping id", group), nil)
		}
	}
}

// validatePython guards the code.python config at save/publish time:
// requirements must be pinned (name==version) so the publish-time
// environment is reproducible and its hash is meaningful.
func validatePython(r *Report, n *Node) {
	if requirements, ok := n.Config["requirements"].([]any); ok {
		for i, raw := range requirements {
			if !validPinnedRequirement(configString(raw)) {
				r.errorf("invalid_python_requirement", n.ID,
					fmt.Sprintf("requirements[%d] %q must be pinned as name==version", i, configString(raw)), nil)
			}
		}
	}
	if v, ok := n.Config["maxMemoryMB"].(float64); ok && (v < 16 || v > 8192) {
		r.errorf("invalid_config", n.ID,
			fmt.Sprintf("maxMemoryMB %v is outside the supported range 16..8192 MiB", v), nil)
	}
}

// validateCode guards the source-or-script rule (Phase 4 D4-1): a code
// node needs one of node-local source (the advanced escape hatch) or a
// reusable scriptRef, resolved to inline source at publish. Setting both
// is legal but the script is ignored — the author should see that before
// publish, not after.
func validateCode(r *Report, n *Node) {
	source := strings.TrimSpace(configString(n.Config["source"]))
	ref := strings.TrimSpace(configString(n.Config["scriptRef"]))
	if source == "" && ref == "" {
		r.errorf("missing_code_source", n.ID,
			fmt.Sprintf("%s needs source or a script reference", n.Type), nil)
		return
	}
	if source != "" && ref != "" {
		r.warnf("script_ref_shadowed", n.ID,
			"node-local source wins over the script reference (advanced escape hatch); clear the source to run the script", nil)
	}
}

// validPinnedRequirement matches the pyenv pinning rule (name==version).
func validPinnedRequirement(req string) bool {
	req = strings.TrimSpace(req)
	if req == "" {
		return false
	}
	eq := strings.Index(req, "==")
	if eq <= 0 || eq == len(req)-2 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := req[i]
		if !(c == '.' || c == '_' || c == '-' || (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	for i := eq + 2; i < len(req); i++ {
		c := req[i]
		if !(c == '.' || c == '_' || c == '-' || c == '+' || c == '!' ||
			(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// configString renders a config value as text; absent values are empty.
func configString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// configNumber reads a config value as a float across the numeric types
// workflows arrive with (JSON float64, hand-built int).
func configNumber(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func validateConfig(r *Report, n *Node, def nodes.Definition, enforceSize bool) {
	defKeys := make([]string, 0, len(def.Config))
	for key := range def.Config {
		defKeys = append(defKeys, key)
	}
	sort.Strings(defKeys)

	for _, key := range defKeys {
		spec := def.Config[key]
		raw, present := n.Config[key]
		if !present {
			if spec.Default != nil {
				raw = spec.Default
			} else {
				if spec.Required {
					r.errorf("missing_config", n.ID, fmt.Sprintf("%s requires config %q", def.Type, key), nil)
				}
				continue
			}
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
		if spec.Pattern != "" {
			if s, ok := raw.(string); ok {
				re, err := regexp.Compile(spec.Pattern)
				if err != nil {
					r.errorf("invalid_config", n.ID, fmt.Sprintf("config %q has an invalid pattern", key), nil)
				} else if !re.MatchString(s) {
					r.errorf("invalid_config", n.ID,
						fmt.Sprintf("config %q must match %s, got %q", key, spec.Pattern, s), nil)
				}
			}
		}
		if numVal, ok := raw.(float64); ok {
			// flow.wait's seconds bound is a runtime-safety limit added at
			// ingest. Stored workflows from before the cap must still load;
			// the runtime rejects an out-of-range value when the node runs.
			enforceBounds := enforceSize || !(n.Type == "flow.wait" && key == "seconds")
			if enforceBounds {
				if spec.Min != nil && numVal < *spec.Min {
					r.errorf("invalid_config", n.ID,
						fmt.Sprintf("config %q must be at least %v, got %v", key, *spec.Min, numVal), nil)
				}
				if spec.Max != nil && numVal > *spec.Max {
					r.errorf("invalid_config", n.ID,
						fmt.Sprintf("config %q must be at most %v, got %v", key, *spec.Max, numVal), nil)
				}
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
			// Connector nodes carry publish-injected internals (the
			// pinned identity and the request template) that no author
			// declares; they are reserved, not authorable.
			if strings.HasPrefix(def.Type, nodes.ConnectorTypePrefix) &&
				(key == "request" || key == "_connector") {
				continue
			}
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
	for key, id := range n.Credentials {
		if !contains(def.Credentials, key) && !contains(def.OptionalCredentials, key) {
			r.warnf("unknown_credential", n.ID, fmt.Sprintf("%s does not use credential %q", def.Type, key), nil)
		}
		if trimmed := strings.TrimSpace(id); trimmed != "" && !secrets.ValidCredentialID(trimmed) {
			r.errorf("invalid_credential", n.ID,
				fmt.Sprintf("credential %q must match [A-Za-z0-9_.-] and may not contain path segments", trimmed), nil)
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
	case nodes.FieldAny:
		return true
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

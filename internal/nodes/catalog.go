package nodes

import "sort"

// PortType is the coarse data contract carried by an edge. Payload JSON Schema
// validation is not implemented; these semantic tokens are the contract that
// is enforced today, so an order can never be wired into a Slack message.
type PortType string

const (
	TypeAny     PortType = "any"
	TypeJSON    PortType = "json"
	TypeText    PortType = "text"
	TypeEvent   PortType = "event"
	TypeOrder   PortType = "order"
	TypeMessage PortType = "message"
	TypeRows    PortType = "rows"
)

func (t PortType) Valid() bool {
	switch t {
	case TypeAny, TypeJSON, TypeText, TypeEvent, TypeOrder, TypeMessage, TypeRows:
		return true
	}
	return false
}

type FieldType string

// ConnectorTypePrefix namespaces connector-authored node types
// (operational-trust Campaign 4): connector.<connectorId>.<op>. It lives
// in nodes so both graph (validation) and store (resolution) can share
// it without import cycles.
const ConnectorTypePrefix = "connector."

const (
	FieldString  FieldType = "string"
	FieldNumber  FieldType = "number"
	FieldBoolean FieldType = "boolean"
	FieldArray   FieldType = "array"
	FieldObject  FieldType = "object"
	FieldAny     FieldType = "any"
)

func (t FieldType) Valid() bool {
	switch t {
	case FieldString, FieldNumber, FieldBoolean, FieldArray, FieldObject, FieldAny:
		return true
	}
	return false
}

type Field struct {
	Type     FieldType `json:"type"`
	Required bool      `json:"required,omitempty"`
	// Default is an effective-value fallback, not authored node state.
	// Save validation and publish apply it only when profile, explicit
	// override, and node config leave the key absent.
	Default any      `json:"default,omitempty"`
	Enum    []string `json:"enum,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	// Label overrides the editor's key-derived label with curated copy.
	Label string `json:"label,omitempty"`
	// Description is a one-line helper shown under the input.
	Description string `json:"description,omitempty"`
	// Placeholder is the input placeholder for text-like fields.
	Placeholder string `json:"placeholder,omitempty"`
	// Advanced moves the field into the inspector's Advanced section.
	Advanced bool `json:"advanced,omitempty"`
	// Secret renders the input as a password field.
	Secret bool `json:"secret,omitempty"`
	// Profileable marks the field as allowed inside a reusable
	// configuration profile (the Configuration Hub). Secret fields are
	// never Profileable; the freeze test enforces the pairing rule.
	Profileable bool `json:"profileable,omitempty"`
}

// Defaults returns the catalog's effective-value fallbacks for a node
// type. The map is freshly allocated so callers may mutate their resolved
// copy without changing the shared catalog.
func Defaults(nodeType string) map[string]any {
	def, ok := catalog[nodeType]
	if !ok || len(def.Config) == 0 {
		return nil
	}
	out := make(map[string]any, len(def.Config))
	for key, field := range def.Config {
		if field.Default != nil {
			out[key] = field.Default
		}
	}
	return out
}

// number returns a pointer to v, for declaring numeric Field bounds.
func number(v float64) *float64 { return &v }

// Definition is the compile-time contract for one node type.
type Definition struct {
	Type        string `json:"type"`
	TypeVersion int    `json:"typeVersion"`
	Category    string `json:"category"`
	// DisplayName is the human-facing name shown in the editor's node
	// library and as the default name for new nodes.
	DisplayName string           `json:"displayName"`
	Description string           `json:"description"`
	Inputs      []string         `json:"inputs"`
	Outputs     []string         `json:"outputs"`
	InputType   PortType         `json:"inputType"`
	OutputType  PortType         `json:"outputType"`
	Config      map[string]Field `json:"config,omitempty"`
	Credentials []string         `json:"credentials,omitempty"`
	// OptionalCredentials are declared so the editor can offer them, but a
	// missing value is valid (for example HTTP auth supplied by headers).
	OptionalCredentials []string `json:"optionalCredentials,omitempty"`
	// OutputTypeKey names a config field that overrides OutputType
	// (used by code.run and data.set to declare what they produce).
	OutputTypeKey string `json:"outputTypeKey,omitempty"`
	// MappingKinds lists the hub mapping kinds this node type consumes
	// (Configuration Hub v3 plan D4): data.map runs item mappings;
	// http.request attaches body/query/headers/pathParams mappings. The
	// hub pages and mapping pickers filter by it instead of hard-coding
	// the mapping on the frontend.
	MappingKinds []string `json:"mappingKinds,omitempty"`
	// Effects describes what this node touches externally and how replay
	// treats it — the EffectPolicy surface shown in the inspector. Empty
	// means the node is pure (no external effects).
	Effects string `json:"effects,omitempty"`
}

func (d Definition) OutputTypeFor(config map[string]any) PortType {
	if d.OutputTypeKey == "" {
		return d.OutputType
	}
	if raw, ok := config[d.OutputTypeKey].(string); ok && raw != "" {
		if t := PortType(raw); t.Valid() {
			return t
		}
	}
	return d.OutputType
}

var catalog = map[string]Definition{
	"trigger.manual": {
		Type: "trigger.manual", DisplayName: "Manual trigger", TypeVersion: 1, Category: "trigger",
		Description: "Start a workflow by hand with optional seed data.",
		Outputs:     []string{"main"}, OutputType: TypeJSON,
		Config: map[string]Field{
			"fields": {Type: FieldArray},
		},
	},
	"trigger.webhook": {
		Type: "trigger.webhook", DisplayName: "Webhook", TypeVersion: 1, Category: "trigger",
		Description: "Start a workflow from an inbound HTTP request.",
		Outputs:     []string{"main"}, OutputType: TypeEvent,
		Config: map[string]Field{
			"path":            {Type: FieldString, Required: true},
			"method":          {Type: FieldString, Required: true, Default: "POST", Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, Profileable: true},
			"secret":          {Type: FieldString, Label: "Shared secret", Secret: true, Advanced: true, Description: "Constant-time compared against the request body signature."},
			"secretHeader":    {Type: FieldString, Label: "Secret header", Advanced: true, Placeholder: "x-loom-secret", Profileable: true},
			"hmacSecret":      {Type: FieldString, Label: "HMAC secret", Secret: true, Advanced: true, Description: "HMAC-SHA256 of the body is verified when set."},
			"hmacHeader":      {Type: FieldString, Label: "HMAC header", Advanced: true, Placeholder: "x-loom-signature", Profileable: true},
			"hmacTimestamps":  {Type: FieldBoolean, Label: "HMAC timestamps", Advanced: true, Description: "Rejects signatures older than five minutes.", Profileable: true},
			"timestampHeader": {Type: FieldString, Label: "Timestamp header", Advanced: true, Placeholder: "x-loom-timestamp", Profileable: true},
			"strictSchema":    {Type: FieldBoolean, Label: "Strict input contract", Advanced: true, Description: "Rejects requests that fail the input contract instead of annotating the trigger item.", Profileable: true},
		},
	},
	"trigger.cron": {
		Type: "trigger.cron", DisplayName: "Schedule", TypeVersion: 1, Category: "trigger",
		Description: "Start a workflow on a schedule.",
		Outputs:     []string{"main"}, OutputType: TypeEvent,
		Config: map[string]Field{
			"schedule": {Type: FieldString, Required: true},
			"timezone": {Type: FieldString},
		},
	},
	"trigger.subflow": {
		Type: "trigger.subflow", DisplayName: "Subflow trigger", TypeVersion: 1, Category: "trigger",
		Description: "Entry point for a workflow invoked by another workflow.",
		Outputs:     []string{"main"}, OutputType: TypeJSON,
	},
	"trigger.event": {
		Type: "trigger.event", DisplayName: "Event trigger", TypeVersion: 1, Category: "trigger",
		Description: "Start the workflow when a matching event is published.",
		Outputs:     []string{"main"}, OutputType: TypeJSON,
		Config: map[string]Field{
			"event": {Type: FieldString, Required: true},
		},
	},
	"flow.emit": {
		Type: "flow.emit", DisplayName: "Emit event", TypeVersion: 1, Category: "action",
		Description: "Publish an event other workflows can subscribe to.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"event":   {Type: FieldString, Required: true},
			"payload": {Type: FieldAny},
		},
	},
	"http.request": {
		Type: "http.request", DisplayName: "HTTP request", TypeVersion: 1, Category: "action",
		Description: "Call an HTTP endpoint.",
		Effects:     "Performs outbound HTTP calls. Every call is recorded on the effect tape and verified on replay; oauth2 auth fetches a bearer token from the credential's token URL (cached in memory, never at rest).",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"method": {Type: FieldString, Required: true, Default: "GET", Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
				Label: "Method", Profileable: true},
			"url": {Type: FieldString, Required: true, Profileable: true,
				Label: "URL", Placeholder: "https://api.example.com/posts/{postId}",
				Description: "Supports {{ }} templating and {name}/:name path placeholders — e.g. /posts/{postId}."},
			"pathParams": {Type: FieldObject, Label: "Path parameters", Advanced: true, Profileable: true,
				Description: "Replaces {name} or :name in the URL, e.g. /posts/{postId} → /posts/1. Path values are never appended automatically; add the placeholder to the URL."},
			"query": {Type: FieldObject, Label: "Query parameters", Profileable: true,
				Description: "Appended to the URL query string automatically; a same-name key already in the URL is overwritten. Avoid putting secrets here."},
			"headers": {Type: FieldObject, Label: "Headers", Profileable: true},
			"bodyType": {Type: FieldString, Default: "none", Enum: []string{"none", "json", "raw", "form", "multipart", "graphql"},
				Label: "Body type", Profileable: true},
			"body": {Type: FieldAny, Label: "Body", Profileable: true,
				Description: "Raw body value; the typed Body fields group compiles into this at publish."},
			"rawBody": {Type: FieldString, Label: "Raw body", Advanced: true,
				Description: "Sent verbatim for raw bodies."},
			"contentType": {Type: FieldString, Label: "Content type", Advanced: true, Profileable: true,
				Placeholder: "text/plain"},
			"parts": {Type: FieldArray, Label: "Multipart parts", Advanced: true,
				Description: "One entry per multipart part."},
			"graphqlQuery":     {Type: FieldString, Label: "GraphQL query", Advanced: true},
			"graphqlVariables": {Type: FieldObject, Label: "GraphQL variables", Advanced: true},
			"auth": {Type: FieldString, Default: "none", Enum: []string{"none", "credential", "bearer", "basic", "apiKey", "oauth2"},
				Label: "Auth", Profileable: true},
			"successCodes": {Type: FieldString, Default: "2xx", Pattern: `^(\s*([1-5]\d\d|[1-5]xx|\d{3}-\d{3})\s*)(,\s*([1-5]\d\d|[1-5]xx|\d{3}-\d{3})\s*)*$`,
				Label: "Success codes", Placeholder: "2xx", Advanced: true, Profileable: true,
				Description: "Comma-separated statuses or ranges treated as success."},
			"failOnError": {Type: FieldBoolean, Default: false, Label: "Fail on error", Advanced: true,
				Description: "Fail the node when the response status is not a success code."},
			"timeout": {Type: FieldNumber, Min: number(1), Max: number(300), Label: "Timeout (seconds)", Advanced: true, Profileable: true},
			"rateLimitPerMinute": {Type: FieldNumber, Min: number(0), Max: number(600), Label: "Rate limit (per minute)", Advanced: true, Profileable: true,
				Description: "Extra calls wait until the limit allows them."},
			"responseMode": {Type: FieldString, Default: "inline", Enum: []string{"inline", "blob", "auto"}, Label: "Response mode", Advanced: true, Profileable: true,
				Description: "Blob mode stores large responses and returns a reference."},
			"inlineThresholdBytes": {Type: FieldNumber, Min: number(1024), Max: number(10485760), Label: "Inline threshold (bytes)", Advanced: true, Profileable: true,
				Description: "Responses larger than this become blobs in auto mode."},
			"pagination": {Type: FieldObject, Label: "Pagination", Advanced: true, Profileable: true,
				Description: "Page/offset/cursor loop over the response."},
			// Parameter groups (Configuration Hub v3 plan D2/D4; v4.1 E4
			// made them Profileable): typed value rows the editor writes
			// and publish compiles into the plain parameter objects below
			// — the engine never sees them. A present fields array wins
			// over the raw object at publish; switching the editor to JSON
			// mode clears the array. Profiles carry the row arrays, so a
			// node's own rows win per name at publish (ResolveProfileRefs
			// merges per row, not per key).
			"bodyFields":   {Type: FieldArray, Label: "Body fields", Advanced: true, Profileable: true, Description: "Typed body rows ({name, mode, value, type}); compiled into body at publish. Wins over body when present."},
			"queryFields":  {Type: FieldArray, Label: "Query fields", Advanced: true, Profileable: true, Description: "Typed rows appended to the URL query automatically — e.g. postId = 1 becomes ?postId=1. Compiled into query at publish; wins over query."},
			"headerFields": {Type: FieldArray, Label: "Header fields", Advanced: true, Profileable: true, Description: "Typed header rows; compiled into headers at publish. Wins over headers when present."},
			"pathFields":   {Type: FieldArray, Label: "Path fields", Advanced: true, Profileable: true, Description: "Typed rows replace {name} or :name in the URL — e.g. add /posts/{postId} to the URL, then a postId row. Values are never appended automatically; compiled into pathParams at publish."},
			"mappingRefs":  {Type: FieldObject, Advanced: true, Description: "Per-group mapping ids ({body, query, headers, pathParams}); publish resolves their rows under the node's own rows."},
		},
		Credentials:         []string{"http"},
		OptionalCredentials: []string{"http", "oauth2"},
		MappingKinds:        []string{"body", "query", "headers", "pathParams"},
	},
	"code.run": {
		Type: "code.run", DisplayName: "Code", TypeVersion: 1, Category: "action",
		Description: "Run a JavaScript step against the incoming item ($http, console, $crypto, $base64 available).",
		Effects:     "Pure by default. $http calls are routed through the guarded host: recorded on the effect tape and verified on replay.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON, OutputTypeKey: "outputType",
		Config: map[string]Field{
			"language": {Type: FieldString, Required: true, Default: "javascript", Enum: []string{"javascript"}, Label: "Language", Profileable: true},
			"scriptRef": {Type: FieldString, Label: "Script",
				Description: "Reusable script id; publish inlines its pinned source. A node-local source below wins when set (advanced escape hatch)."},
			"source": {Type: FieldString, Label: "Source", Placeholder: "return $json;",
				Description: "Node-local source — the advanced escape hatch. Leave empty when a script reference supplies the code."},
			"outputType":     {Type: FieldString, Label: "Output type", Advanced: true, Profileable: true},
			"timeoutSeconds": {Type: FieldNumber, Default: 2.0, Min: number(1), Max: number(300), Label: "Timeout (seconds)", Advanced: true, Profileable: true},
		},
	},
	"code.python": {
		Type: "code.python", DisplayName: "Python", TypeVersion: 1, Category: "action",
		Description: "Run a Python step in a subprocess against the incoming item (opt-in: --enable-subprocess-nodes).",
		Effects:     "Executes a Python subprocess — the single largest security surface in Loom; needs the operator opt-in. Each execution is recorded on the effect tape (exit status, output, result) and verified on replay without re-running anything. Third-party dependencies are built at publish time into hash-keyed environments; runs never touch the network.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"scriptRef": {Type: FieldString, Label: "Script",
				Description: "Reusable script id; publish inlines its pinned source, requirements and runtime defaults. A node-local source below wins when set (advanced escape hatch)."},
			"source": {Type: FieldString,
				Label: "Python source", Placeholder: "return {'ok': True}",
				Description: "Node-local source — the advanced escape hatch. Leave empty when a script reference supplies the code."},
			"requirements": {Type: FieldArray, Label: "Requirements", Profileable: true,
				Description: "Pinned entries like \"idna==3.10\"; unpinned names are rejected at save time. Runs in a subprocess with a per-node time and memory cap."},
			"timeoutSeconds": {Type: FieldNumber, Default: 60.0, Min: number(1), Max: number(900), Label: "Timeout (seconds)", Advanced: true, Profileable: true},
			"maxMemoryMB":    {Type: FieldNumber, Default: 512.0, Min: number(16), Max: number(8192), Label: "Memory limit (MB)", Advanced: true, Profileable: true},
		},
	},
	"data.set": {
		Type: "data.set", DisplayName: "Set data", TypeVersion: 1, Category: "action",
		Description: "Build the outgoing item from explicit fields.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON, OutputTypeKey: "outputType",
		Config: map[string]Field{
			"values":      {Type: FieldObject, Required: true},
			"keepOnlySet": {Type: FieldBoolean},
			"outputType":  {Type: FieldString, Enum: []string{"json", "text", "event", "order", "message", "rows"}},
		},
	},
	"data.map": {
		Type: "data.map", DisplayName: "Map data", TypeVersion: 1, Category: "action",
		Description: "Transform the item through a reusable mapping (target ← source rows).",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON, OutputTypeKey: "outputType",
		Config: map[string]Field{
			"mappingRef":  {Type: FieldString, Required: true, Description: "Hub mapping whose rows run here; overrides below win per target."},
			"mappingRows": {Type: FieldArray, Advanced: true, Description: "Resolved mapping rows — publish fills this from the mapping; edits here are overwritten at publish."},
			"overrides":   {Type: FieldObject, Description: "Per-target source expressions overriding the mapping rows."},
			"outputType":  {Type: FieldString, Enum: []string{"json", "text", "event", "order", "message", "rows"}},
		},
		MappingKinds: []string{"item"},
	},
	"flow.if": {
		Type: "flow.if", DisplayName: "If", TypeVersion: 1, Category: "logic",
		Description: "Split the flow on a boolean expression.",
		Inputs:      []string{"main"}, Outputs: []string{"true", "false"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"condition": {Type: FieldString, Required: true},
		},
	},
	"flow.switch": {
		Type: "flow.switch", DisplayName: "Switch", TypeVersion: 1, Category: "logic",
		Description: "Route to one of three cases or a default.",
		Inputs:      []string{"main"}, Outputs: []string{"case0", "case1", "case2", "default"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"cases": {Type: FieldArray, Required: true},
		},
	},
	"flow.merge": {
		Type: "flow.merge", DisplayName: "Merge", TypeVersion: 1, Category: "logic",
		Description: "Join two branches into one.",
		Inputs:      []string{"a", "b"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"mode": {Type: FieldString, Default: "shallow", Enum: []string{"shallow", "append", "byIndex"}},
		},
	},
	"flow.loop": {
		Type: "flow.loop", DisplayName: "Loop", TypeVersion: 1, Category: "logic",
		Description: "Run a sub-workflow once per item, then continue.",
		Inputs:      []string{"main"}, Outputs: []string{"done"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"workflowId":    {Type: FieldString, Required: true},
			"itemsPath":     {Type: FieldString},
			"maxIterations": {Type: FieldNumber},
			"concurrency":   {Type: FieldNumber},
		},
	},
	"flow.wait": {
		Type: "flow.wait", DisplayName: "Wait", TypeVersion: 1, Category: "logic",
		Description: "Pause the run for a fixed number of seconds.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"seconds": {Type: FieldNumber, Required: true, Min: number(1), Max: number(2592000)},
		},
	},
	"flow.approval": {
		Type: "flow.approval", DisplayName: "Approval", TypeVersion: 1, Category: "logic",
		Description: "Pause the run until a human approves or rejects.",
		Inputs:      []string{"main"}, Outputs: []string{"approved", "rejected"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"channel": {Type: FieldString, Label: "Channel", Advanced: true, Profileable: true,
				Description: "Where the approval request is delivered (e.g. a chat channel id)."},
			"message": {Type: FieldString, Required: true,
				Label: "Message", Placeholder: "Approve this run?",
				Description: "Shown to the approver with the run context."},
			"timeoutSeconds": {Type: FieldNumber, Label: "Timeout (seconds)", Advanced: true, Profileable: true,
				Description: "When the wait times out, the on-timeout behavior applies."},
			"onTimeout": {Type: FieldString, Enum: []string{"rejected", "approved"}, Label: "On timeout", Advanced: true, Profileable: true,
				Description: "Decision recorded when the wait times out."},
			"linkExpiresMinutes": {Type: FieldNumber, Label: "Link expiry (minutes)", Advanced: true, Profileable: true,
				Description: "How long the approval link stays valid."},
			"reminderMinutes": {Type: FieldNumber, Label: "Reminder (minutes)", Advanced: true, Profileable: true,
				Description: "Send a reminder after this many minutes of waiting."},
			"credential": {Type: FieldString, Label: "Notify credential", Advanced: true,
				Description: "Credential used for the notify URL delivery."},
			"notifyUrl": {Type: FieldString, Label: "Notify URL", Advanced: true,
				Description: "Webhook that receives the approval request."},
		},
	},
	"flow.subflow": {
		Type: "flow.subflow", DisplayName: "Subflow", TypeVersion: 1, Category: "logic",
		Description: "Invoke another workflow.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"workflowId": {Type: FieldString, Required: true},
			"wait":       {Type: FieldBoolean, Default: true},
		},
	},
	"db.postgres": {
		Type: "db.postgres", DisplayName: "Postgres", TypeVersion: 1, Category: "action",
		Description: "Run a SQL statement and return rows.",
		Effects:     "Runs SQL against the configured database. Statements are recorded on the effect tape and verified on replay.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeRows,
		Config: map[string]Field{
			"operation": {Type: FieldString, Default: "query", Enum: []string{"query", "insert", "update"}, Profileable: true,
				Label: "Operation"},
			"query": {Type: FieldString, Required: true,
				Label: "SQL", Placeholder: "select 1 as ok",
				Description: "Parameterized statements only — never interpolate values."},
		},
		Credentials: []string{"postgres"},
	},
	"data.json.parse": {
		Type: "data.json.parse", DisplayName: "Parse JSON", TypeVersion: 1, Category: "action",
		Description: "Parse a JSON string into an item.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"text": {Type: FieldString, Required: true},
		},
	},
	"text.template": {
		Type: "text.template", DisplayName: "Text template", TypeVersion: 1, Category: "action",
		Description: "Render a text template.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeText,
		Config: map[string]Field{
			"template": {Type: FieldString, Required: true},
		},
	},
}

func Get(nodeType string) (Definition, bool) {
	d, ok := catalog[nodeType]
	return d, ok
}

func All() []Definition {
	out := make([]Definition, 0, len(catalog))
	for _, d := range catalog {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

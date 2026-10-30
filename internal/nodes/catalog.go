package nodes

import "sort"

// PortType is the coarse data contract carried by an edge.
// JSON Schema validation of the payload itself lands in M1; M0 enforces
// these semantic tokens so an order can never be wired into a Slack message.
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

const (
	FieldString  FieldType = "string"
	FieldNumber  FieldType = "number"
	FieldBoolean FieldType = "boolean"
	FieldArray   FieldType = "array"
	FieldObject  FieldType = "object"
)

func (t FieldType) Valid() bool {
	switch t {
	case FieldString, FieldNumber, FieldBoolean, FieldArray, FieldObject:
		return true
	}
	return false
}

type Field struct {
	Type     FieldType `json:"type"`
	Required bool      `json:"required,omitempty"`
	Enum     []string  `json:"enum,omitempty"`
}

// Definition is the compile-time contract for one node type.
type Definition struct {
	Type        string           `json:"type"`
	TypeVersion int              `json:"typeVersion"`
	Category    string           `json:"category"`
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
		Type: "trigger.manual", TypeVersion: 1, Category: "trigger",
		Description: "Start a workflow by hand with optional seed data.",
		Outputs:     []string{"main"}, OutputType: TypeJSON,
		Config: map[string]Field{
			"fields": {Type: FieldArray},
		},
	},
	"trigger.webhook": {
		Type: "trigger.webhook", TypeVersion: 1, Category: "trigger",
		Description: "Start a workflow from an inbound HTTP request.",
		Outputs:     []string{"main"}, OutputType: TypeEvent,
		Config: map[string]Field{
			"path":         {Type: FieldString, Required: true},
			"method":       {Type: FieldString, Required: true, Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
			"secret":       {Type: FieldString},
			"secretHeader": {Type: FieldString},
			"hmacSecret":   {Type: FieldString},
			"hmacHeader":   {Type: FieldString},
		},
	},
	"trigger.cron": {
		Type: "trigger.cron", TypeVersion: 1, Category: "trigger",
		Description: "Start a workflow on a schedule.",
		Outputs:     []string{"main"}, OutputType: TypeEvent,
		Config: map[string]Field{
			"schedule": {Type: FieldString, Required: true},
			"timezone": {Type: FieldString},
		},
	},
	"trigger.subflow": {
		Type: "trigger.subflow", TypeVersion: 1, Category: "trigger",
		Description: "Entry point for a workflow invoked by another workflow.",
		Outputs:     []string{"main"}, OutputType: TypeJSON,
	},
	"http.request": {
		Type: "http.request", TypeVersion: 1, Category: "action",
		Description: "Call an HTTP endpoint.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"method":       {Type: FieldString, Required: true, Enum: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
			"url":          {Type: FieldString, Required: true},
			"pathParams":   {Type: FieldObject},
			"query":        {Type: FieldObject},
			"headers":      {Type: FieldObject},
			"bodyType":     {Type: FieldString, Enum: []string{"none", "json", "raw", "form"}},
			"body":         {Type: FieldObject},
			"rawBody":      {Type: FieldString},
			"contentType":  {Type: FieldString},
			"auth":         {Type: FieldString, Enum: []string{"none", "credential", "bearer", "basic", "apiKey"}},
			"successCodes": {Type: FieldString},
			"failOnError":  {Type: FieldBoolean},
			"timeout":      {Type: FieldNumber},
		},
		Credentials:         []string{"http"},
		OptionalCredentials: []string{"http"},
	},
	"code.run": {
		Type: "code.run", TypeVersion: 1, Category: "action",
		Description: "Run a JavaScript step against the incoming item.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON, OutputTypeKey: "outputType",
		Config: map[string]Field{
			"language":   {Type: FieldString, Required: true, Enum: []string{"javascript"}},
			"source":     {Type: FieldString, Required: true},
			"outputType": {Type: FieldString, Enum: []string{"json", "text", "event", "order", "message", "rows"}},
		},
	},
	"data.set": {
		Type: "data.set", TypeVersion: 1, Category: "action",
		Description: "Build the outgoing item from explicit fields.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON, OutputTypeKey: "outputType",
		Config: map[string]Field{
			"values":      {Type: FieldObject, Required: true},
			"keepOnlySet": {Type: FieldBoolean},
			"outputType":  {Type: FieldString, Enum: []string{"json", "text", "event", "order", "message", "rows"}},
		},
	},
	"flow.if": {
		Type: "flow.if", TypeVersion: 1, Category: "logic",
		Description: "Split the flow on a boolean expression.",
		Inputs:      []string{"main"}, Outputs: []string{"true", "false"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"condition": {Type: FieldString, Required: true},
		},
	},
	"flow.switch": {
		Type: "flow.switch", TypeVersion: 1, Category: "logic",
		Description: "Route to one of three cases or a default.",
		Inputs:      []string{"main"}, Outputs: []string{"case0", "case1", "case2", "default"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"cases": {Type: FieldArray, Required: true},
		},
	},
	"flow.merge": {
		Type: "flow.merge", TypeVersion: 1, Category: "logic",
		Description: "Join two branches into one.",
		Inputs:      []string{"a", "b"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"mode": {Type: FieldString, Enum: []string{"append", "byIndex"}},
		},
	},
	"flow.loop": {
		Type: "flow.loop", TypeVersion: 1, Category: "logic",
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
		Type: "flow.wait", TypeVersion: 1, Category: "logic",
		Description: "Pause the run for a fixed number of seconds.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"seconds": {Type: FieldNumber, Required: true},
		},
	},
	"flow.approval": {
		Type: "flow.approval", TypeVersion: 1, Category: "logic",
		Description: "Pause the run until a human approves or rejects.",
		Inputs:      []string{"main"}, Outputs: []string{"approved", "rejected"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"channel":        {Type: FieldString},
			"message":        {Type: FieldString, Required: true},
			"timeoutSeconds": {Type: FieldNumber},
		},
	},
	"flow.subflow": {
		Type: "flow.subflow", TypeVersion: 1, Category: "logic",
		Description: "Invoke another workflow.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"workflowId": {Type: FieldString, Required: true},
			"wait":       {Type: FieldBoolean},
		},
	},
	"db.postgres": {
		Type: "db.postgres", TypeVersion: 1, Category: "action",
		Description: "Run a SQL statement and return rows.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeRows,
		Config: map[string]Field{
			"operation": {Type: FieldString, Required: true, Enum: []string{"query", "insert", "update"}},
			"query":     {Type: FieldString, Required: true},
		},
		Credentials: []string{"postgres"},
	},
	"chat.slack": {
		Type: "chat.slack", TypeVersion: 1, Category: "action",
		Description: "Send a message to Slack.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeMessage, OutputType: TypeJSON,
		Config: map[string]Field{
			"channel": {Type: FieldString, Required: true},
			"text":    {Type: FieldString, Required: true},
		},
		Credentials: []string{"slack"},
	},
	"data.json.parse": {
		Type: "data.json.parse", TypeVersion: 1, Category: "action",
		Description: "Parse a JSON string into an item.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeJSON, OutputType: TypeJSON,
		Config: map[string]Field{
			"text": {Type: FieldString, Required: true},
		},
	},
	"data.csv.parse": {
		Type: "data.csv.parse", TypeVersion: 1, Category: "action",
		Description: "Parse CSV text into row objects.",
		Inputs:      []string{"main"}, Outputs: []string{"main"},
		InputType: TypeText, OutputType: TypeRows,
		Config: map[string]Field{
			"text":      {Type: FieldString, Required: true},
			"delimiter": {Type: FieldString},
			"headers":   {Type: FieldBoolean},
		},
	},
	"text.template": {
		Type: "text.template", TypeVersion: 1, Category: "action",
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

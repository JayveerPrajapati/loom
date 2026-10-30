package graph

// Workflow is the on-disk, versioned graph. It is deliberately plain JSON:
// the same bytes are the editor document, the API payload and the test fixture.
type Workflow struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Version  int            `json:"version"`
	Nodes    []Node         `json:"nodes"`
	Edges    []Edge         `json:"edges"`
	Settings map[string]any `json:"settings,omitempty"`
}

type Node struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	TypeVersion int               `json:"typeVersion"`
	Name        string            `json:"name"`
	Position    Position          `json:"position"`
	Config      map[string]any    `json:"config"`
	Credentials map[string]string `json:"credentials,omitempty"`
	Retry       *RetryPolicy      `json:"retry,omitempty"`
	OnError     string            `json:"onError,omitempty"`
	Batch       *BatchPolicy      `json:"batch,omitempty"`
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

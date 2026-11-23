package nodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"loom/internal/flow"
	"loom/internal/pyenv"
)

// pythonNode executes Python source in a subprocess (code.python). The
// execution is a recorded effect: replays serve the taped outcome and never
// re-run user code. Dependencies are resolved at publish time into hash-keyed
// environments; runs never install anything.
type pythonNode struct{}

func (pythonNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	source := str(in.Config["source"])
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("code.python: source is required")
	}
	seconds := num(in.Config["timeoutSeconds"])
	if seconds == 0 {
		seconds = 60
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 900 {
		seconds = 900
	}
	memoryMB := num(in.Config["maxMemoryMB"])
	if memoryMB == 0 {
		memoryMB = 512
	}
	if memoryMB < 16 {
		memoryMB = 16
	}
	if memoryMB > 8192 {
		memoryMB = 8192
	}
	var envKey string
	if requirements, ok := in.Config["requirements"].([]any); ok && len(requirements) > 0 {
		pinned := make([]string, 0, len(requirements))
		for _, raw := range requirements {
			req := strings.TrimSpace(fmt.Sprint(raw))
			if !pyenv.ValidRequirement(req) {
				return nil, fmt.Errorf("code.python: requirement %q must be pinned as name==version", req)
			}
			pinned = append(pinned, req)
		}
		envKey = pyenv.Key(pinned)
	}

	executor, ok := in.Host.(flow.Executor)
	if !ok {
		return nil, fmt.Errorf("code.python: this host cannot execute subprocesses (live runs need --enable-subprocess-nodes; replays verify from the recorded tape)")
	}
	res, err := executor.Exec(ctx, flow.ExecRequest{
		Source: source, Stdin: in.Item, EnvKey: envKey,
		Timeout:     time.Duration(seconds * float64(time.Second)),
		MaxMemoryMB: int(memoryMB),
	})
	if err != nil {
		return nil, fmt.Errorf("code.python: %w", err)
	}
	trace(in, "exec", map[string]any{
		"exitCode": res.ExitCode, "stdout": previewValue(flow.Item{"text": res.Stdout}),
		"stderr": previewValue(flow.Item{"text": res.Stderr}), "env": envKey,
	})
	return flow.Output{"main": pythonResult(res.Result)}, nil
}

// pythonResult shapes the script's return value with the same convention
// as code.run: objects become the item, arrays become {"items": [...]},
// scalars become {"value": ...}, and nothing becomes {}.
func pythonResult(result any) flow.Item {
	switch value := result.(type) {
	case map[string]any:
		return flow.Item(value)
	case []any:
		return flow.Item{"items": value}
	case nil:
		return flow.Item{}
	default:
		return flow.Item{"value": value}
	}
}

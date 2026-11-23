package engine

import (
	"context"
	"fmt"

	"loom/internal/egress"
	"loom/internal/flow"
)

// workflowEgressHost enforces a workflow's own egress allowlist (the
// doc-level `egress` block) on every outbound HTTP call of that run,
// including subflow runs (the subflow's own policy applies to it).
//
// Capability forwarding (the wireEventBus lesson): a plain embedded
// flow.Host drops the optional capability interfaces nodes assert at
// runtime — BlobSaver/BlobLoader (http.request blob modes), Executor
// (code.python), EventPublisher (emit). The engine-side wrapper is
// capability-conditional: it only exists when the workflow declares an
// egress policy, and it forwards every optional interface the base
// host implements.
type workflowEgressHost struct {
	flow.Host
	policy  *egress.WorkflowPolicy
	exec    func(ctx context.Context, req flow.ExecRequest) (flow.ExecResult, error)
	save    func(data []byte) (hash string, size int, err error)
	load    func(hash string) ([]byte, error)
	publish func(ev flow.Event)
}

func wrapWorkflowEgress(base flow.Host, policy *egress.WorkflowPolicy) flow.Host {
	if policy == nil || len(policy.Allowlist) == 0 {
		return base
	}
	w := &workflowEgressHost{Host: base, policy: policy}
	if exec, ok := base.(flow.Executor); ok {
		w.exec = exec.Exec
	}
	if saver, ok := base.(flow.BlobSaver); ok {
		w.save = saver.SaveBlob
	}
	if loader, ok := base.(flow.BlobLoader); ok {
		w.load = loader.LoadBlob
	}
	if publisher, ok := base.(flow.EventPublisher); ok {
		w.publish = publisher.Publish
	}
	return w
}

func (w *workflowEgressHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	dest, err := egress.ParseDestination(req.URL)
	if err != nil {
		return flow.HTTPResponse{}, err
	}
	if err := w.policy.Check(dest); err != nil {
		return flow.HTTPResponse{}, fmt.Errorf("workflow egress: %w", err)
	}
	return w.Host.HTTP(ctx, req)
}

func (w *workflowEgressHost) Exec(ctx context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	if w.exec == nil {
		return flow.ExecResult{}, fmt.Errorf("egress wrapper: host does not execute subprocesses")
	}
	return w.exec(ctx, req)
}

func (w *workflowEgressHost) SaveBlob(data []byte) (string, int, error) {
	if w.save == nil {
		return "", 0, fmt.Errorf("egress wrapper: host does not save blobs")
	}
	return w.save(data)
}

func (w *workflowEgressHost) LoadBlob(hash string) ([]byte, error) {
	if w.load == nil {
		return nil, fmt.Errorf("egress wrapper: host does not load blobs")
	}
	return w.load(hash)
}

func (w *workflowEgressHost) Publish(ev flow.Event) {
	if w.publish != nil {
		w.publish(ev)
	}
}

import { Canvas, type NodeStatus } from '../Canvas';
import { formatDurationMs, type TimelineRow } from '../runDetailHelpers';
import type { NodeDef, Workflow } from '../../types';

// The execution-canvas panel (Phase 6 D6-2): graph snapshot, node
// statuses, and the playback scrubber for terminal runs. Props-only.
//
// L3: at phone width the graph is replaced by a legible step list built
// from the same timeline rows the dock already renders — the two-inch
// canvas and its blank minimap read worse than no graph at all. Tapping
// a step opens the existing data pane for that node.
export function RunCanvasPanel(props: {
  workflow: Workflow | null;
  workflowName: string;
  defs: Record<string, NodeDef>;
  statuses: Record<string, NodeStatus>;
  selectedNodeId: string | null;
  templateVersion?: number;
  /** True at phone width (≤720px): render the step list, not the graph. */
  phone: boolean;
  timeline: TimelineRow[];
  now: number;
  playback: {
    active: boolean;
    playing: boolean;
    playhead: number;
    totalMs: number;
    scrubbing: boolean;
    onToggle: () => void;
    onScrub: (value: number) => void;
    onFullRun: () => void;
  };
  onSelect: (nodeId: string | null) => void;
}) {
  const { workflow, statuses, playback, phone, timeline, now } = props;
  return (
    <section className="run-canvas-panel" aria-label="Execution canvas">
      <header className="run-canvas-head">
        <div>
          <h2>{workflow?.name ?? props.workflowName}</h2>
        </div>
        <div className="run-canvas-meta">
          <span className="muted small">
            {phone
              ? timeline.length > 0
                ? `${timeline.length} steps`
                : 'Waiting for node events…'
              : workflow
                ? `${workflow.nodes.length} nodes`
                : 'Loading graph…'}
          </span>
          <span className="version-chip">
            {props.templateVersion ? `v${props.templateVersion}` : 'snapshot'}
          </span>
        </div>
      </header>
      <div className="run-canvas-body">
        {phone ? (
          <div className="run-step-list" data-testid="run-step-list">
            {timeline.length === 0 && <p className="muted small pad">Waiting for node events…</p>}
            {timeline.map((row) => (
              <button
                type="button"
                className={`timeline-row status-${row.status} ${
                  props.selectedNodeId === row.nodeId ? 'active' : ''
                }`}
                key={row.nodeId}
                onClick={() => props.onSelect(row.nodeId)}
              >
                <span className={`run-step-status status-text status-${row.status}`}>
                  {row.status}
                </span>
                <span className="timeline-main">
                  <strong>{row.name}</strong>
                  <em>
                    {row.nodeId}
                    {row.attempts > 1 ? `, ${row.attempts} attempts` : ''}
                  </em>
                  {row.error && <span className="timeline-error">{row.error}</span>}
                </span>
                <span className="timeline-duration">
                  {formatDurationMs(
                    row.status === 'running' && row.start
                      ? Math.max(0, now - row.start)
                      : row.durationMs,
                  )}
                </span>
              </button>
            ))}
          </div>
        ) : workflow ? (
          <Canvas
            workflow={workflow}
            defs={props.defs}
            statuses={statuses}
            issues={[]}
            selectedId={props.selectedNodeId}
            onSelect={props.onSelect}
            onMove={() => undefined}
            onDeleteNodes={() => undefined}
            onDeleteEdges={() => undefined}
            onConnect={() => undefined}
            onAddNode={() => undefined}
            readOnly
          />
        ) : (
          <p className="muted small pad">The workflow snapshot for this run is unavailable.</p>
        )}
      </div>
      {playback.active && (
        <div className="run-playback">
          <button
            type="button"
            className="icon-button"
            onClick={playback.onToggle}
            aria-label={playback.playing ? 'Pause playback' : 'Play run'}
          >
            {playback.playing ? 'Pause' : 'Play'}
          </button>
          <input
            type="range"
            min={0}
            max={playback.totalMs}
            step={Math.max(1, Math.round(playback.totalMs / 200))}
            value={Math.min(playback.playhead, playback.totalMs)}
            aria-label="Playback position"
            onChange={(event) => playback.onScrub(Number(event.target.value))}
          />
          <span className="run-playback-time">
            {formatDurationMs(playback.playhead)} / {formatDurationMs(playback.totalMs)}
          </span>
          {playback.scrubbing && (
            <button type="button" className="text-button" onClick={playback.onFullRun}>
              Full run
            </button>
          )}
        </div>
      )}
    </section>
  );
}

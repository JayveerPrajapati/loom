import type { Dispatch, RefObject, SetStateAction } from 'react';
import type { Job, ReplayResult, Run, RunEvent, Workflow } from '../types';
import { type DockTab, type Filter, type TimelineRow } from './runDetailHelpers';
import { DataTab } from './runDock/DataTab';
import { RawTab } from './runDock/RawTab';
import { TimelineTab } from './runDock/TimelineTab';

export interface DecisionHistoryEntry {
  event: RunEvent;
  data: { decision: string; actor: string; source: string; note?: string };
}

export interface StepDetail {
  loading: boolean;
  input?: unknown;
  output?: unknown;
  effects?: unknown;
  config?: Record<string, unknown>;
  credentials?: Record<string, string>;
  error?: string;
}

export interface ReplayState {
  busy: boolean;
  result?: ReplayResult;
  error?: string;
  hermetic?: boolean;
}

interface Props {
  dock: DockTab;
  setDock: Dispatch<SetStateAction<DockTab>>;
  events: RunEvent[];
  // Timeline tab
  follow: boolean;
  setFollow: Dispatch<SetStateAction<boolean>>;
  timeline: TimelineRow[];
  decisionEvents: DecisionHistoryEntry[];
  selectedNodeId: string | null;
  selectNode: (nodeId: string) => void;
  timelineEndRef: RefObject<HTMLDivElement>;
  run: Run;
  workflowName: string;
  job: Job | null;
  children: Run[];
  now: number;
  onOpenTemplate: (id: string) => void;
  onOpenJob: (id: string) => void;
  onOpenRun: (id: string) => void;
  // Data tab
  selectedEvent: RunEvent | null;
  setSelectedEvent: Dispatch<SetStateAction<RunEvent | null>>;
  setSelectedNodeId: Dispatch<SetStateAction<string | null>>;
  activeEvent: RunEvent | null;
  selectedNode: Workflow['nodes'][number] | null;
  nodeName: (nodeId?: string) => string;
  activeStatus: string | undefined;
  detail: StepDetail;
  request: unknown;
  response: unknown;
  inputDraft: string | null;
  setInputDraft: Dispatch<SetStateAction<string | null>>;
  replay: ReplayState;
  busy: string;
  hermetic: boolean;
  setHermetic: Dispatch<SetStateAction<boolean>>;
  replayStep: (event: RunEvent) => Promise<void>;
  replayStepLive: (event: RunEvent) => Promise<void>;
  replayFrom: (event: RunEvent) => Promise<void>;
  replayIsStep: boolean;
  // Raw tab
  filter: Filter;
  setFilter: Dispatch<SetStateAction<Filter>>;
  query: string;
  setQuery: Dispatch<SetStateAction<string>>;
  copyLog: () => Promise<void>;
  copied: boolean;
  downloadLog: () => void;
  visibleEvents: RunEvent[];
  selectEvent: (event: RunEvent) => void;
}

// The run dock — Timeline / Data / Raw events tabs. All state and
// callbacks stay in the page; each tab body is a props-only leaf.
export function RunDock(props: Props) {
  const { dock, setDock, events } = props;
  return (
    <aside className="run-dock" aria-label="Run details">
      <div className="run-dock-tabs" role="tablist" aria-label="Run details">
        <button
          type="button"
          role="tab"
          aria-selected={dock === 'timeline'}
          className={dock === 'timeline' ? 'active' : ''}
          onClick={() => setDock('timeline')}
        >
          Timeline
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={dock === 'data'}
          className={dock === 'data' ? 'active' : ''}
          onClick={() => setDock('data')}
        >
          Data
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={dock === 'raw'}
          className={dock === 'raw' ? 'active' : ''}
          onClick={() => setDock('raw')}
        >
          Raw events <span>{events.length}</span>
        </button>
      </div>

      <div className="run-dock-body">
        {dock === 'timeline' && <TimelineTab {...props} />}
        {dock === 'data' && <DataTab {...props} />}
        {dock === 'raw' && <RawTab {...props} />}
      </div>
    </aside>
  );
}

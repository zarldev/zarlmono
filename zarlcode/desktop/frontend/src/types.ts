import type {
  CacheTargetDiagnostics as NativeCacheTargetDiagnostics,
  SessionUsageSnapshot as NativeSessionUsageSnapshot,
} from "./bindings/github.com/zarldev/zarlmono/zarlcode/tui/models.js";

// Native session display contract; credentials never appear in snapshots.
export interface Step {
  step: string;
  status: string;
}
export interface Entry {
  id: string;
  parentId: string;
  turnId: string;
  kind: string;
  payload: {
    text?: string;
    complete?: boolean;
    tool_name?: string;
    tool_id?: string;
    argument?: string;
    effect?: string;
    tool_state?: string;
    duration_ms?: number;
    path?: string;
    diff?: string;
    plan?: { steps: Step[] };
    agent_name?: string;
    prompt?: string;
    subagent?: string;
    control?: {
      version: number;
      id: string;
      policy_label: string;
      system_digest: string;
      surface_digest: string;
      tool_definitions?: { name: string; definition_hash: string }[];
    };
  };
}

// Generated native contracts remain authoritative. Partial keeps old persisted
// snapshots and focused fixtures compatible without constructing runtime models.
export type CacheTargetDiagnostics = Partial<NativeCacheTargetDiagnostics> & {
  pricing_tier_unknown_tasks?: number;
};

export type SessionUsageSnapshot = Partial<
  Omit<NativeSessionUsageSnapshot, "cache_targets">
> & {
  cache_targets?: CacheTargetDiagnostics[] | null;
  cost_accounting_version?: number;
  legacy_cost_usd?: number;
  legacy_cost_parent_usd?: number;
  legacy_cache_saved_usd?: number;
};
export interface Snapshot {
  revision: string;
  sessionId: string;
  workspace: string;
  provider: string;
  model: string;
  mode: string;
  state: string;
  error: string;
  draft: string;
  omitted: number;
  unchanged?: boolean;
  retrySaveReason?: string;
  exportPath?: string;
  recoveryError?: string;
  usage?: SessionUsageSnapshot;
  entries: Entry[];
}
export interface Command {
  action: "snapshot" | "submit" | "stop" | "mode" | "model" | "retry-save" | "draft" | "export-recovery";
  text?: string;
  provider?: string;
  model?: string;
  since?: string;
}
export interface ConnectOptions {
  passphrase: string;
  resume: boolean;
  allowUnconfined: boolean;
}
export interface Transport {
  connect(options: ConnectOptions): Promise<Snapshot>;
  command(command: Command): Promise<Snapshot>;
}

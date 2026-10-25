import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { transport } from "./transport";
import type {
  CacheTargetDiagnostics,
  Entry,
  Snapshot,
  Command,
  SessionUsageSnapshot,
  Transport,
} from "./types";
import { Placeholder } from "./Placeholder";
import { MarkdownMessage } from "./MarkdownMessage";
import { conversationRows, activityLabel } from "./conversation";
import { controlChangeLabel, controlChangeSummary } from "./ui/controller-workbench-renderers";
import { useFollowScroll } from "./useFollowScroll";

const EMPTY: Snapshot = {
  revision: "0",
  sessionId: "",
  workspace: "",
  provider: "",
  model: "",
  mode: "build",
  state: "disconnected",
  error: "",
  draft: "",
  omitted: 0,
  entries: [],
};
const title = (entry: Entry) =>
  entry.payload.tool_name ||
  entry.payload.agent_name ||
  entry.payload.path ||
  ({
    user_message: "You",
    assistant_message: "zarlcode",
    plan: "Plan updated",
    diff: "File changed",
  }[entry.kind] ??
    (entry.kind === "control_change" ? controlChangeLabel(entry) : entry.kind));

export function App({ api = transport }: { api?: Transport }) {
  const [session, setSession] = useState(EMPTY);
  const [connected, setConnected] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [draft, setDraft] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [resume, setResume] = useState(false);
  const [allowUnconfined, setAllowUnconfined] = useState(false);
  const [selected, setSelected] = useState("");
  const [agent, setAgent] = useState("");
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState<"evidence" | "changes">("evidence");
  const [settings, setSettings] = useState(false);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const generation = useRef(0);
  const revision = useRef("");
  const running = useRef(false);
  const mounted = useRef(true);
  const inFlight = useRef(false);
  const draftWrites = useRef(Promise.resolve());
  const draftSequence = useRef(0);
  const editDraft = (text: string) => {
    setDraft(text);
    const edit = ++draftSequence.current;
    generation.current++;
    draftWrites.current = draftWrites.current.then(async () => {
      try {
        const next = await api.command({ action: "draft", text });
        if (mounted.current && edit === draftSequence.current) accept(next);
      } catch (cause) {
        if (mounted.current) setError(String(cause));
      }
    });
  };
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      generation.current++;
    };
  }, []);
  const accept = useCallback((next: Snapshot) => {
    if (next.unchanged) return;
    revision.current = next.revision;
    running.current = next.state === "running" || next.state === "cancelling";
    setSession((previous) =>
      JSON.stringify(previous) === JSON.stringify(next) ? previous : next,
    );
  }, []);
  const connect = async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError("");
    const password = passphrase;
    setPassphrase("");
    try {
      const next = await api.connect({
        passphrase: password,
        resume,
        allowUnconfined,
      });
      if (mounted.current) {
        accept(next);
        setDraft(next.draft);
        setProvider(next.provider);
        setModel(next.model);
        setConnected(true);
      }
    } catch (cause) {
      if (mounted.current) setError(String(cause));
    } finally {
      inFlight.current = false;
      if (mounted.current) setBusy(false);
    }
  };
  const command = async (request: Command) => {
    if (inFlight.current) return false;
    inFlight.current = true;
    generation.current++;
    setBusy(true);
    setError("");
    try {
      await draftWrites.current;
      const next = await api.command(request);
      if (mounted.current) accept(next);
      return true;
    } catch (cause) {
      if (mounted.current) setError(String(cause));
      return false;
    } finally {
      inFlight.current = false;
      if (mounted.current) setBusy(false);
    }
  };
  // One outstanding poll; commands supersede stale snapshots. Execution and
  // persistence remain in Go even if this window reloads or stops polling.
  useEffect(() => {
    if (!connected) return;
    let stopped = false;
    let timer: number;
    const poll = async () => {
      const request = generation.current;
      try {
        if (!inFlight.current) {
          const next = await api.command({
            action: "snapshot",
            since: revision.current,
          });
          if (!stopped && mounted.current && request === generation.current)
            accept(next);
        }
      } catch (cause) {
        if (!stopped) setError(String(cause));
      } finally {
        if (!stopped)
          timer = window.setTimeout(poll, running.current ? 80 : 400);
      }
    };
    timer = window.setTimeout(poll, 250);
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [connected, api, accept]);
  const active = [
    "running",
    "cancelling",
    "saving",
    "configuring",
    "closing",
    "exporting",
  ].includes(session.state);
  const submitBlocked =
    active || session.state === "route-error" || session.state === "unsaved";
  const submit = async () => {
    if (!draft.trim() || submitBlocked || busy || !connected) return;
    const submitted = draft;
    if (await command({ action: "submit", text: submitted }))
      setDraft((current) => (current === submitted ? "" : current));
  };
  const plan =
    session.entries
      .slice()
      .reverse()
      .find((e) => e.kind === "plan")?.payload.plan?.steps ?? [];
  const agents = session.entries.filter((e) => e.kind === "subagent");
  const diffs = session.entries.filter((e) => e.kind === "diff");
  const tools = session.entries.filter((e) => e.kind === "tool_call");
  const inspected = session.entries.find((e) => e.id === selected);
  const descendants = useMemo(() => {
    const ids = new Set([agent]);
    for (const entry of session.entries)
      if (ids.has(entry.parentId)) ids.add(entry.id);
    return ids;
  }, [session.entries, agent]);
  const entries = useMemo(
    () =>
      session.entries.filter(
        (e) =>
          (!agent || descendants.has(e.id)) &&
          (!query ||
            `${title(e)} ${e.payload.text ?? ""} ${e.payload.argument ?? ""} ${e.payload.effect ?? ""} ${e.payload.diff ?? ""}`
              .toLowerCase()
              .includes(query.toLowerCase())),
      ),
    [session.entries, agent, descendants, query],
  );
  const select = (id: string) => {
    setSelected(id);
    setTab("evidence");
  };
  const displayError = error || session.error;
  const workspaceName =
    session.workspace.split("/").filter(Boolean).at(-1) || "Workspace";

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark">z</span>
          <strong>zarlcode</strong>
          <span className="muted">/</span>
          <span title={session.workspace}>{workspaceName}</span>
        </div>
        <div className="top-context">
          {session.provider}
          {session.model && ` / ${session.model}`}
        </div>
        <div className="session-controls">
          <span className={`state-dot ${active ? "active" : ""}`} />
          <span>{busy && !connected ? "Connecting" : session.state}</span>
          {connected && (
            <>
              <select
                aria-label="Mode"
                value={session.mode}
                disabled={busy || active}
                onChange={(e) =>
                  void command({ action: "mode", text: e.target.value })
                }
              >
                <option value="build">Build</option>
                <option value="plan">Plan</option>
              </select>
              <button
                onClick={() => setSettings(!settings)}
                aria-label="Model settings"
              >
                ⚙
              </button>
            </>
          )}
        </div>
      </header>
      {!connected ? (
        <div className="connect-screen">
          <div className="connect-card">
            <span className="brand-mark">z</span>
            <h1>Open workspace</h1>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void connect();
              }}
            >
              <label>
                Vault passphrase
                <input
                  type="password"
                  autoComplete="off"
                  value={passphrase}
                  onChange={(e) => setPassphrase(e.target.value)}
                />
              </label>
              <label className="check-label">
                <input
                  type="checkbox"
                  checked={resume}
                  onChange={(e) => setResume(e.target.checked)}
                />
                Continue last session
              </label>
              {displayError.includes("unconfined") && (
                <label className="check-label consent">
                  <input
                    type="checkbox"
                    checked={allowUnconfined}
                    onChange={(e) => setAllowUnconfined(e.target.checked)}
                  />
                  Allow shell commands without OS confinement
                </label>
              )}
              {displayError && (
                <div role="alert" className="error">
                  {displayError}
                </div>
              )}
              <button className="primary" disabled={busy}>
                {busy ? "Connecting…" : "Connect"}
              </button>
            </form>
          </div>
        </div>
      ) : (
        <>
          {settings && (
            <form
              className="model-settings"
              onSubmit={(e) => {
                e.preventDefault();
                void command({ action: "model", provider, model }).then(
                  (ok) => {
                    if (ok) setSettings(false);
                  },
                );
              }}
            >
              <label>
                Provider
                <input
                  value={provider}
                  onChange={(e) => setProvider(e.target.value)}
                />
              </label>
              <label>
                Model
                <input
                  value={model}
                  onChange={(e) => setModel(e.target.value)}
                />
              </label>
              <button disabled={active || busy}>Apply</button>
              <button type="button" onClick={() => setSettings(false)}>
                Cancel
              </button>
            </form>
          )}
          <main className="workspace">
            <aside className="sidebar" aria-label="Plan and agents">
              <div className="pane-heading">
                <h2>Task</h2>
              </div>
              <section className="side-section">
                <div className="section-label">
                  PLAN{" "}
                  <span>
                    {plan.filter((p) => p.status === "completed").length}/
                    {plan.length}
                  </span>
                </div>
                {plan.length ? (
                  <ol className="plan">
                    {plan.map((step, i) => (
                      <li key={i} className={step.status}>
                        <span className="step-number">
                          {step.status === "completed"
                            ? "✓"
                            : String(i + 1).padStart(2, "0")}
                        </span>
                        <span>{step.step}</span>
                      </li>
                    ))}
                  </ol>
                ) : (
                  <Placeholder shape="plan" label="No plan" />
                )}
              </section>
              <section className="side-section">
                <div className="section-label">
                  AGENTS <span>{agents.length + 1}</span>
                </div>
                <button
                  className={`agent ${!agent ? "selected" : ""}`}
                  onClick={() => setAgent("")}
                >
                  <span className="avatar">Z</span>
                  <span>
                    <strong>zarlcode</strong>
                    <small>Primary</small>
                  </span>
                </button>
                {agents.map((a) => (
                  <button
                    key={a.id}
                    className={`agent child ${agent === a.id ? "selected" : ""}`}
                    onClick={() => setAgent(a.id)}
                  >
                    <span className="avatar secondary">◇</span>
                    <span>
                      <strong>{a.payload.agent_name}</strong>
                      <small>{a.payload.subagent}</small>
                    </span>
                  </button>
                ))}
              </section>
              <section className="side-section">
                <div className="section-label">
                  CHANGES <span>{diffs.length}</span>
                </div>
                {diffs.map((d) => (
                  <button
                    className="file-link"
                    key={d.id}
                    onClick={() => select(d.id)}
                  >
                    <span className="accent">M</span>
                    <span>{d.payload.path}</span>
                  </button>
                ))}
                {!diffs.length && (
                  <Placeholder shape="files" label="No changes" />
                )}
              </section>
            </aside>
            <section className="conversation" aria-label="Conversation">
              <div className="pane-heading">
                <h2>Conversation</h2>
                <span>{entries.length.toLocaleString()} entries</span>
              </div>
              <div className="searchbar">
                <span aria-hidden="true">⌕</span>
                <input
                  aria-label="Filter conversation"
                  placeholder="Filter…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                />
                {agent && (
                  <button
                    aria-label="Clear agent filter"
                    onClick={() => setAgent("")}
                  >
                    Clear ×
                  </button>
                )}
              </div>
              {(displayError || session.state === 'unsaved') && (
                <div className="error" role="alert">
                  {displayError}
                  {session.state === "unsaved" && !session.retrySaveReason && (
                    <button
                      onClick={() => void command({ action: "retry-save" })}
                    >
                      Retry save
                    </button>
                  )}
                  {session.state === 'unsaved' && <>
                    {session.retrySaveReason && <div>{session.retrySaveReason}</div>}
                    <button disabled={busy} onClick={()=>void command({action:'export-recovery'})}>Export recovery copy</button>
                  </>}
                </div>
              )}
              {session.exportPath && <div className="history-limit" role="status">Recovery copy: <code>{session.exportPath}</code></div>}
              {session.recoveryError && <div className="error" role="alert">{session.recoveryError}</div>}
              {session.omitted > 0 && (
                <div className="history-limit">
                  {session.omitted} earlier entries not displayed
                </div>
              )}
              <Timeline
                focused={!!agent}
                filtered={!!query || !!agent}
                entries={entries}
                selected={selected}
                onSelect={select}
                streaming={session.state === "running"}
              />
              <form
                className="live-composer"
                onSubmit={(e) => {
                  e.preventDefault();
                  void submit();
                }}
              >
                <textarea
                  aria-label="Prompt"
                  placeholder="Ask anything, or give it a task…"
                  value={draft}
                  maxLength={262144}
                  disabled={busy}
                  onChange={(e) => editDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (
                      e.key === "Enter" &&
                      (e.metaKey || e.ctrlKey) &&
                      !e.nativeEvent.isComposing
                    ) {
                      e.preventDefault();
                      void submit();
                    }
                  }}
                />
                <div>
                  <span>{session.mode === "plan" ? "Plan" : "Build"}</span>
                  {active ? (
                    <button
                      type="button"
                      className="stop"
                      disabled={
                        busy ||
                        session.state === "cancelling" ||
                        session.state === "saving"
                      }
                      onClick={() => void command({ action: "stop" })}
                    >
                      ■ Stop
                    </button>
                  ) : (
                    <button
                      className="primary"
                      disabled={
                        busy || !draft.trim() || submitBlocked
                      }
                    >
                      Send ↗
                    </button>
                  )}
                </div>
              </form>
            </section>
            <aside className="inspector" aria-label="Inspector">
              <div className="pane-heading">
                <h2>Inspector</h2>
              </div>
              <div className="tabs">
                <button
                  className={tab === "evidence" ? "active-tab" : ""}
                  onClick={() => setTab("evidence")}
                >
                  Evidence
                </button>
                <button
                  className={tab === "changes" ? "active-tab" : ""}
                  onClick={() => setTab("changes")}
                >
                  Changes <span>{diffs.length}</span>
                </button>
              </div>
              <CacheDiagnostics usage={session.usage} />
              <div className="inspector-body">
                {tab === "changes" ? (
                  <>
                    {diffs.length ? (
                      diffs.map((d) => (
                        <button
                          key={d.id}
                          className="file-link"
                          onClick={() => select(d.id)}
                        >
                          <span className="accent">±</span>
                          {d.payload.path}
                        </button>
                      ))
                    ) : (
                      <Placeholder shape="files" label="No changes" />
                    )}
                  </>
                ) : inspected ? (
                  <Evidence entry={inspected} />
                ) : (
                  <>
                    {!tools.length && (
                      <Placeholder
                        shape="inspector"
                        label="No event selected"
                      />
                    )}
                    <div className="evidence-list">
                      {tools.map((t) => (
                        <button key={t.id} onClick={() => select(t.id)}>
                          <span>↗ {t.payload.tool_name}</span>
                          <small>{t.payload.tool_state}</small>
                        </button>
                      ))}
                    </div>
                  </>
                )}
              </div>
            </aside>
          </main>
          <div className="statusbar">
            <span>
              <span className="dot" />
              {session.workspace}
            </span>
            <span>
              {session.sessionId
                ? session.sessionId.slice(0, 8)
                : "New session"}{" "}
              · {session.state}
            </span>
          </div>
        </>
      )}
    </div>
  );
}

const tokens = new Intl.NumberFormat("en-US");

function billingLabel(billingClass: string) {
  switch (billingClass) {
    case "local":
      return "Local · no API billing";
    case "subscription":
      return "Subscription · no metered API price";
    case "metered":
      return "Metered API";
    default:
      return "Billing class unknown";
  }
}

function dollars(value: number) {
  if (value > 0 && value < 0.0001) return "<$0.0001";
  return `$${value.toFixed(4)}`;
}

function hasHistoricalUsage(usage: SessionUsageSnapshot) {
  return Boolean(
    usage.turns ||
      usage.in ||
      usage.out ||
      usage.cached ||
      usage.cache_creation_tokens ||
      usage.cost_usd ||
      usage.cache_targets?.length,
  );
}

function CacheDiagnostics({ usage }: { usage?: SessionUsageSnapshot }) {
  if (!usage) return null;
  const targets = usage.cache_targets ?? [];
  const knownSubtotal = targets.reduce(
    (total, target) => total + (target.known_cost_usd ?? 0),
    0,
  );
  const legacy =
    usage.cache_diagnostics_legacy ||
    (usage.cache_diagnostics_version !== 1 && hasHistoricalUsage(usage));

  return (
    <details className="cache-diagnostics">
      <summary>
        <span>Cache diagnostics</span>
        <small>
          {targets.length ? `${targets.length} model${targets.length === 1 ? "" : "s"}` : "No attributed tasks"}
          {knownSubtotal > 0 && ` · ${dollars(knownSubtotal)} base-rate estimate`}
        </small>
      </summary>
      <div className="cache-diagnostics-body">
        <p className="cache-disclaimer">
          Read share describes reported prompt tokens, not cache-hit causality. Cost
          figures cover fresh input/output at available static base rates only and
          never estimate saved dollars.
        </p>
        {legacy && (
          <p className="cache-warning">
            Legacy unattributed history: earlier usage cannot be assigned to a
            provider/model.
          </p>
        )}
        {targets.map((target) => (
          <CacheTarget key={`${target.provider}/${target.model}/${target.billing_class}`} target={target} />
        ))}
        {!targets.length && !legacy && (
          <p className="cache-empty">No settled task usage yet.</p>
        )}
        {targets.length > 0 && (
          <p className="cache-subtotal">
            Base-rate estimate subtotal <strong>{dollars(knownSubtotal)}</strong>
            <span>Fresh input/output with available static rates only</span>
          </p>
        )}
      </div>
    </details>
  );
}

function CacheTarget({ target }: { target: CacheTargetDiagnostics }) {
  const readUnknown = target.cache_read_rate_unknown_tasks ?? 0;
  const writeUnknown = target.cache_write_rate_unknown_tasks ?? 0;
  const tierUnknown = target.pricing_tier_unknown_tasks ?? 0;
  const provider = target.provider || "unknown provider";
  const model = target.model || "unknown model";
  const billingClass = target.billing_class ?? "";
  return (
    <section className="cache-target">
      <header>
        <strong>{provider} / {model}</strong>
        <span>{billingLabel(billingClass)}</span>
      </header>
      <dl>
        <div><dt>Tasks</dt><dd>{tokens.format(target.settled_tasks ?? 0)} ({target.root_tasks ?? 0} root · {target.child_tasks ?? 0} child)</dd></div>
        <div><dt>Tokens</dt><dd>{tokens.format(target.prompt_tokens ?? 0)} prompt · {tokens.format(target.completion_tokens ?? 0)} completion</dd></div>
        <div><dt>Cache</dt><dd>{tokens.format(target.cache_read_tokens ?? 0)} read · {tokens.format(target.cache_creation_tokens ?? 0)} created</dd></div>
        <div><dt>Creation TTL</dt><dd>{tokens.format(target.cache_creation_5m_tokens ?? 0)} 5m · {tokens.format(target.cache_creation_1h_tokens ?? 0)} 1h · {tokens.format(target.cache_creation_unknown_ttl ?? 0)} unknown TTL</dd></div>
        <div><dt>Read share</dt><dd>{target.cache_read_share_known ? `${((target.cache_read_share ?? 0) * 100).toFixed(1)}% of reported prompt tokens` : "Unavailable · zero prompt-token denominator"}</dd></div>
      </dl>
      {(target.missing_usage_tasks ?? 0) > 0 && <p>{target.missing_usage_tasks} task(s) missing usage reports.</p>}
      {(target.reported_zero_usage_tasks ?? 0) > 0 && <p>{target.reported_zero_usage_tasks} task(s) reported zero usage.</p>}
      {(target.base_rate_unknown_tasks ?? 0) > 0 && <p>Base pricing unavailable for {target.base_rate_unknown_tasks} task(s).</p>}
      {tierUnknown > 0 && <p>Prompt pricing tier unavailable for {tierUnknown} task(s); the base-rate estimate is incomplete.</p>}
      {(readUnknown > 0 || writeUnknown > 0) && (
        <p>Cache pricing unavailable: {readUnknown} read task(s), {writeUnknown} write task(s).</p>
      )}
    </section>
  );
}

function Timeline({
  entries,
  selected,
  onSelect,
  streaming,
  focused,
  filtered,
}: {
  entries: Entry[];
  selected: string;
  onSelect: (id: string) => void;
  streaming: boolean;
  focused: boolean;
  filtered: boolean;
}) {
  const rows = useMemo(
    () => conversationRows(entries, focused),
    [entries, focused],
  );
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const { viewport, content, onScroll, atBottom, jump } = useFollowScroll();
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => viewport.current,
    estimateSize: (i) => (rows[i].kind === "activity" ? 34 : 95),
    overscan: 6,
    getItemKey: (i) => rows[i].id,
  });
  return (
    <div className="timeline-wrap">
      <div
        className="timeline"
        ref={viewport}
        onScroll={onScroll}
        tabIndex={0}
        aria-label="Conversation history"
      >
        {!rows.length && (
          <Placeholder
            shape="conversation"
            label={filtered ? "No matches" : ""}
          />
        )}
        <div
          className="virtual-space"
          ref={content}
          style={{ height: virtualizer.getTotalSize() }}
        >
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index];
            return (
              <div
                key={item.key}
                data-index={item.index}
                ref={virtualizer.measureElement}
                className="virtual-row"
                style={{ transform: `translateY(${item.start}px)` }}
              >
                {row.kind === "message" ? (
                  <article
                    className={`entry ${row.entry.kind}`}
                    data-testid="transcript-entry"
                  >
                    <div className="entry-heading">
                      <strong>
                        {row.entry.kind === "user_message"
                          ? "You"
                          : focused
                            ? "Agent"
                            : "zarlcode"}
                      </strong>
                    </div>
                    {row.entry.kind === "assistant_message" ? (
                      <MarkdownMessage
                        text={row.entry.payload.text ?? ""}
                        streaming={streaming && !row.entry.payload.complete}
                      />
                    ) : (
                      <div className="message-text">
                        {row.entry.payload.text}
                      </div>
                    )}
                  </article>
                ) : (
                  <div className="activity" data-testid="activity-group">
                    <button
                      className={`activity-toggle ${row.entries.some((e) => e.payload.tool_state === "failed") ? "has-failure" : ""}`}
                      aria-expanded={expanded.has(row.id)}
                      onClick={() =>
                        setExpanded((previous) => {
                          const next = new Set(previous);
                          if (next.has(row.id)) next.delete(row.id);
                          else next.add(row.id);
                          return next;
                        })
                      }
                    >
                      <span aria-hidden="true">
                        {expanded.has(row.id) ? "▾" : "▸"}
                      </span>
                      {activityLabel(row.entries)}
                    </button>
                    {expanded.has(row.id) && (
                      <div className="activity-items">
                        {row.entries.map((entry) => (
                          <button
                            key={entry.id}
                            className={`activity-item ${selected === entry.id ? "selected" : ""}`}
                            onClick={() => onSelect(entry.id)}
                          >
                            <span className="activity-name">
                              {title(entry)}
                            </span>
                            <span
                              className={`activity-state ${entry.payload.tool_state === "failed" ? "failed" : ""}`}
                            >
                              {entry.payload.tool_state ||
                                entry.payload.subagent ||
                                (entry.kind === "control_change"
                                  ? controlChangeSummary(entry)
                                  : "")}
                            </span>
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>
      {!atBottom && (
        <button className="jump" onClick={jump}>
          ↓ Jump to latest
        </button>
      )}
    </div>
  );
}

function Evidence({ entry }: { entry: Entry }) {
  const [copyState, setCopyState] = useState("Copy");
  const text =
    entry.payload.diff ||
    entry.payload.effect ||
    entry.payload.text ||
    entry.payload.prompt ||
    (entry.kind === "control_change" ? controlChangeSummary(entry) : "") ||
    "";
  useEffect(() => setCopyState("Copy"), [entry.id]);
  return (
    <>
      <div className="evidence-title">
        <span className="eyebrow">{entry.kind.replaceAll("_", " ")}</span>
        <h3>{title(entry)}</h3>
        <span className="caption">{entry.payload.tool_state}</span>
      </div>
      <button
        className="copy"
        onClick={async () => {
          try {
            await navigator.clipboard.writeText(text);
            setCopyState("Copied");
          } catch {
            setCopyState("Select text to copy");
          }
        }}
      >
        {copyState}
      </button>
      {entry.payload.argument && (
        <>
          <h4>INPUT</h4>
          <pre className="input-block">{entry.payload.argument}</pre>
        </>
      )}
      <h4>{entry.payload.diff ? "DIFF" : "OUTPUT"}</h4>
      {entry.payload.diff ? (
        <pre className="diff">
          {text.split("\n").map((line, i) => (
            <span
              key={i}
              className={
                line.startsWith("+")
                  ? "addition"
                  : line.startsWith("-")
                    ? "deletion"
                    : line.startsWith("@@")
                      ? "hunk"
                      : ""
              }
            >
              <span className="line-number">{i + 1}</span>
              {line}
              {"\n"}
            </span>
          ))}
        </pre>
      ) : text ? (
        <pre className="output-block">{text}</pre>
      ) : (
        <Placeholder shape="output" label="No tool output" />
      )}
    </>
  );
}

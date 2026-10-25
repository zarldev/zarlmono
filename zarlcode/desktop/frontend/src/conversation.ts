import type { Entry } from "./types";

export type ConversationRow =
  | { id: string; kind: "message"; entry: Entry }
  | { id: string; kind: "activity"; entries: Entry[] };

// Preserve chronology while reducing implementation events to one expandable
// activity row between replies. Focused agent views keep that agent's prose.
export function conversationRows(
  entries: Entry[],
  focused: boolean,
): ConversationRow[] {
  const rows: ConversationRow[] = [];
  for (const entry of entries) {
    const message =
      entry.kind === "user_message" ||
      (entry.kind === "assistant_message" && (!entry.parentId || focused));
    if (message) {
      rows.push({ id: entry.id, kind: "message", entry });
      continue;
    }
    const last = rows.at(-1);
    if (last?.kind === "activity") last.entries.push(entry);
    else rows.push({ id: entry.id, kind: "activity", entries: [entry] });
  }
  return rows;
}

export function activityLabel(entries: Entry[]): string {
  const calls = entries.filter((e) => e.kind === "tool_call");
  const failed = calls.filter((e) => e.payload.tool_state === "failed").length;
  const active =
    calls.some((e) => e.payload.tool_state === "running") ||
    entries.some(
      (e) => e.kind === "subagent" && e.payload.subagent === "running",
    );
  const count = calls.length;
  const label = count
    ? `${count} tool call${count === 1 ? "" : "s"}`
    : "Activity";
  return `${label}${active ? " · running" : ""}${failed ? ` · ${failed} failed` : ""}`;
}

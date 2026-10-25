import type { Entry } from "../types";

const shortDigest = (digest = "") => digest.slice(0, 12);

export function controlChangeLabel(entry: Entry): string {
  const control = entry.payload.control;
  if (!control) return "Control change";
  const tools = control.tool_definitions?.length ?? 0;
  return `Control · ${control.policy_label} · ${tools} tool${tools === 1 ? "" : "s"}`;
}

export function controlChangeSummary(entry: Entry): string {
  const control = entry.payload.control;
  if (!control) return "";
  return `system ${shortDigest(control.system_digest)} · surface ${shortDigest(control.surface_digest)}`;
}

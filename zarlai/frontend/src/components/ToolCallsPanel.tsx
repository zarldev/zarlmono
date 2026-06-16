import { useState } from 'react'
import type { ToolCallEntry } from '@/hooks/usePresenceSession'
import { FloatingPanel } from './FloatingPanel'

interface Props {
  calls: ToolCallEntry[]
  isOpen: boolean
  onClose: () => void
}

// Draggable panel showing recent tool calls. Summary row per call
// (name, provider, duration, ok/error); click to expand args + result.
export default function ToolCallsPanel({ calls, isOpen, onClose }: Props) {
  const [expandedAt, setExpandedAt] = useState<number | null>(null)

  return (
    <FloatingPanel
      id="tool-calls"
      title="tool calls"
      isOpen={isOpen}
      onClose={onClose}
      defaultCorner="tr"
      width={420}
      maxHeight="70vh"
      accessory={<span className="text-white/40 tabular-nums">{calls.length.toString().padStart(2, '0')}</span>}
    >
      {calls.length === 0 ? (
        <p className="text-[11px] text-white/40 p-4">no tool calls yet this session.</p>
      ) : (
        <ul className="flex-1 overflow-y-auto p-2 space-y-1.5 min-h-0">
          {calls.map((c) => {
            const expanded = expandedAt === c.at
            const failed = !!c.error
            return (
              <li key={c.at} className="rounded border border-white/5 bg-white/[0.02]">
                <button
                  onClick={() => setExpandedAt(expanded ? null : c.at)}
                  className="w-full flex items-baseline gap-2 px-2.5 py-1.5 text-left hover:bg-white/[0.03] transition-colors"
                >
                  <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${failed ? 'bg-red-400' : 'bg-emerald-400'}`} />
                  <span className="text-[12px] font-mono font-medium truncate text-white">{c.name}</span>
                  {c.provider && (
                    <span className="text-[10px] text-white/40 truncate">{c.provider}</span>
                  )}
                  <span className="ml-auto text-[10px] text-white/40 tabular-nums shrink-0">{c.duration_ms}ms</span>
                </button>
                {expanded && (
                  <div className="px-2.5 pb-2 pt-0.5 space-y-1 border-t border-white/5">
                    <DetailRow label="at" value={new Date(c.at).toLocaleTimeString([], { hour12: false })} />
                    {c.args && <DetailBlock label="args" body={prettyJSON(c.args)} />}
                    {c.result && <DetailBlock label="result" body={c.result} />}
                    {c.error && <DetailBlock label="error" body={c.error} errorish />}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </FloatingPanel>
  )
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline gap-2 text-[10px]">
      <span className="op-mono tracking-wider text-white/30 w-12 shrink-0">{label}</span>
      <span className="text-white/60 font-mono">{value}</span>
    </div>
  )
}

function DetailBlock({ label, body, errorish }: { label: string; body: string; errorish?: boolean }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="op-mono tracking-wider text-white/30 text-[10px]">{label}</span>
      <pre
        className={`text-[10px] leading-snug font-mono whitespace-pre-wrap break-words p-1.5 rounded bg-black/30 ${errorish ? 'text-red-300' : 'text-white/75'}`}
        style={{ maxHeight: 160, overflowY: 'auto' }}
      >{body}</pre>
    </div>
  )
}

function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

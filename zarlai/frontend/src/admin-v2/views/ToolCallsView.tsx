import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import { ListToolCallsRequestSchema, type ToolCallMsg } from '@/gen/zarl/v1/admin_pb'
import { client, prettyJSON } from '@/admin/shared'
import Drawer from '../Drawer'
import { T } from '../tokens'
import { Card, MicroLabel } from '../primitives'

type Result = 'all' | 'errors' | 'success'

export default function ToolCallsView() {
  const [offset, setOffset] = useState(0)
  const limit = 50
  const [tool, setTool] = useState<string>('')
  const [provider, setProvider] = useState<string>('')
  const [result, setResult] = useState<Result>('all')
  const [selected, setSelected] = useState<ToolCallMsg | null>(null)

  const q = useQuery({
    queryKey: ['toolCalls', offset],
    queryFn: () => client.listToolCalls(create(ListToolCallsRequestSchema, { limit, offset })),
  })

  const all = q.data?.calls ?? []
  const tools     = useMemo(() => Array.from(new Set(all.map(c => c.toolName))).filter(Boolean).sort(), [all])
  const providers = useMemo(() => Array.from(new Set(all.map(c => c.provider))).filter(Boolean).sort(), [all])

  const rows = all.filter(c =>
    (tool === ''     || c.toolName === tool) &&
    (provider === '' || c.provider === provider) &&
    (result === 'all' || (result === 'errors' ? c.error !== '' : c.error === ''))
  )

  const total = q.data?.total ?? 0

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <label className="text-[11px]" style={{ color: T.textDim }}>Tool</label>
        <select
          value={tool}
          onChange={e => setTool(e.target.value)}
          className="text-[12px] px-2 py-1 rounded bg-transparent border"
          style={{ color: T.text, borderColor: T.border }}
        >
          <option value="">All tools</option>
          {tools.map(t => <option key={t} value={t}>{t}</option>)}
        </select>

        <label className="text-[11px] ml-3" style={{ color: T.textDim }}>Provider</label>
        <select
          value={provider}
          onChange={e => setProvider(e.target.value)}
          className="text-[12px] px-2 py-1 rounded bg-transparent border"
          style={{ color: T.text, borderColor: T.border }}
        >
          <option value="">All providers</option>
          {providers.map(p => <option key={p} value={p}>{p}</option>)}
        </select>

        <label className="text-[11px] ml-3" style={{ color: T.textDim }}>Result</label>
        <select
          value={result}
          onChange={e => setResult(e.target.value as Result)}
          className="text-[12px] px-2 py-1 rounded bg-transparent border"
          style={{ color: T.text, borderColor: T.border }}
        >
          <option value="all">All</option>
          <option value="errors">Errors only</option>
          <option value="success">Success only</option>
        </select>
      </div>

      <MicroLabel>Tool calls ({rows.length})</MicroLabel>
      <Card padding="flush">
        <table className="w-full text-[12px] tabular-nums">
          <thead>
            <tr className="text-left" style={{ color: T.textDim }}>
              <th className="font-normal px-3 py-2">Tool</th>
              <th className="font-normal px-3 py-2">Provider</th>
              <th className="font-normal px-3 py-2 text-right">Duration</th>
              <th className="font-normal px-3 py-2">Status</th>
              <th className="font-normal px-3 py-2">Time</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr><td colSpan={5} className="px-3 py-4 text-center" style={{ color: T.textDim }}>No calls match filters.</td></tr>
            )}
            {rows.map(c => {
              const ok = !c.error
              return (
                <tr
                  key={c.id}
                  onClick={() => setSelected(c)}
                  className="border-t cursor-pointer hover:bg-white/[0.02]"
                  style={{ borderColor: T.border, color: T.text }}
                >
                  <td className="px-3 py-2" style={{ color: T.textBright }}>{c.toolName}</td>
                  <td className="px-3 py-2">{c.provider}</td>
                  <td className="px-3 py-2 text-right">{c.durationMs}ms</td>
                  <td className="px-3 py-2">
                    <span className="inline-block w-1.5 h-1.5 rounded-full mr-1.5" style={{ background: ok ? T.status.ok : T.status.error }} />
                    {ok ? 'ok' : 'error'}
                  </td>
                  <td className="px-3 py-2">{c.createdAt}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </Card>

      {total > limit && (
        <div className="flex items-center gap-3 justify-end text-[12px]" style={{ color: T.textDim }}>
          <span>{offset + 1}–{Math.min(offset + limit, total)} of {total}</span>
          <button onClick={() => setOffset(Math.max(0, offset - limit))} disabled={offset === 0}
                  className="px-3 py-1 rounded border disabled:opacity-30" style={{ borderColor: T.border }}>Prev</button>
          <button onClick={() => setOffset(offset + limit)} disabled={offset + limit >= total}
                  className="px-3 py-1 rounded border disabled:opacity-30" style={{ borderColor: T.border }}>Next</button>
        </div>
      )}

      <Drawer
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected && (
          <div className="flex flex-col gap-1">
            <span className="text-[18px] font-semibold">{selected.toolName}</span>
            <span className="text-[11px]" style={{ color: T.textDim }}>{selected.provider} · {selected.createdAt}</span>
          </div>
        )}
      >
        {selected && (
          <div className="flex flex-col gap-4 text-[12px]" style={{ color: T.text }}>
            <div>
              <div className="text-[10px] uppercase tracking-[0.12em] mb-1" style={{ color: T.textDim }}>Session</div>
              <code className="font-mono text-[11px]">{selected.sessionId}</code>
            </div>
            <Section label="Args"><pre className="font-mono text-[11px] whitespace-pre-wrap">{prettyJSON(selected.args)}</pre></Section>
            <Section label="Result"><pre className="font-mono text-[11px] whitespace-pre-wrap">{prettyJSON(selected.result)}</pre></Section>
            {selected.error && (
              <Section label="Error">
                <pre className="font-mono text-[11px] whitespace-pre-wrap" style={{ color: T.status.error }}>{selected.error}</pre>
              </Section>
            )}
          </div>
        )}
      </Drawer>
    </div>
  )
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-[0.12em] mb-1" style={{ color: T.textDim }}>{label}</div>
      <div className="rounded bg-black/30 p-2 border" style={{ borderColor: T.border }}>{children}</div>
    </div>
  )
}

import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import { ListConversationSummariesRequestSchema, type ConversationSummaryMsg } from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import Drawer from '../Drawer'
import { T } from '../tokens'
import { Card, MicroLabel } from '../primitives'

export default function ChatsView() {
  const [person, setPerson] = useState<string>('')
  const [selected, setSelected] = useState<ConversationSummaryMsg | null>(null)

  const q = useQuery({
    queryKey: ['conversation-summaries', person],
    queryFn: () => client.listConversationSummaries(create(ListConversationSummariesRequestSchema, {
      personName: person, limit: 100, offset: 0,
    })),
  })

  const rows = q.data?.summaries ?? []
  const total = q.data?.total ?? 0
  const persons = useMemo(() => Array.from(new Set(rows.map(s => s.personName))).filter(Boolean), [rows])

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <label className="text-[11px]" style={{ color: T.textDim }}>Person</label>
        <select
          value={person}
          onChange={e => setPerson(e.target.value)}
          className="text-[12px] px-2 py-1 rounded bg-transparent border"
          style={{ color: T.text, borderColor: T.border }}
        >
          <option value="">All people</option>
          {persons.map(p => <option key={p} value={p}>{p}</option>)}
        </select>
        <span className="ml-auto text-[11px]" style={{ color: T.textDim }}>{total} total</span>
      </div>

      <MicroLabel>Chats ({rows.length})</MicroLabel>
      <Card padding="flush">
        <table className="w-full text-[12px]">
          <thead>
            <tr className="text-left" style={{ color: T.textDim }}>
              <th className="font-normal px-3 py-2">Person</th>
              <th className="font-normal px-3 py-2">Session</th>
              <th className="font-normal px-3 py-2">Created</th>
              <th className="font-normal px-3 py-2">Summary</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr><td colSpan={4} className="px-3 py-4 text-center" style={{ color: T.textDim }}>No summaries.</td></tr>
            )}
            {rows.map(s => (
              <tr key={s.id} onClick={() => setSelected(s)}
                  className="border-t cursor-pointer hover:bg-white/[0.02]"
                  style={{ borderColor: T.border, color: T.text }}>
                <td className="px-3 py-2" style={{ color: T.textBright }}>{s.personName || '—'}</td>
                <td className="px-3 py-2 font-mono text-[11px]">{s.sessionId.slice(0, 8)}</td>
                <td className="px-3 py-2 tabular-nums">{s.createdAt}</td>
                <td className="px-3 py-2 truncate max-w-[400px]">{s.summary.split('\n')[0]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      <Drawer
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected && (
          <div className="flex flex-col gap-1">
            <span className="text-[18px] font-semibold">{selected.personName || '—'}</span>
            <span className="text-[11px] font-mono" style={{ color: T.textDim }}>{selected.sessionId}</span>
          </div>
        )}
      >
        {selected && (
          <pre className="text-[12px] whitespace-pre-wrap leading-relaxed" style={{ color: T.text }}>{selected.summary}</pre>
        )}
      </Drawer>
    </div>
  )
}

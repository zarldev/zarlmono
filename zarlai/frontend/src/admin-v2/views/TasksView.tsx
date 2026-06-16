import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListTasksRequestSchema,
  CancelTaskRequestSchema,
  DeleteTaskRequestSchema,
  type TaskMsg,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import Drawer from '../Drawer'
import { T } from '../tokens'
import { Card, MicroLabel } from '../primitives'

type StatusFilter = 'all' | 'pending' | 'running' | 'completed' | 'failed'

export default function TasksView() {
  const qc = useQueryClient()
  const [offset, setOffset] = useState(0)
  const [status, setStatus] = useState<StatusFilter>('all')
  const [selected, setSelected] = useState<TaskMsg | null>(null)
  const limit = 50

  const q = useQuery({
    queryKey: ['tasks', offset],
    queryFn: () => client.listTasks(create(ListTasksRequestSchema, { limit, offset })),
  })

  const cancelMut = useMutation({
    mutationFn: (id: string) => client.cancelTask(create(CancelTaskRequestSchema, { id })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['tasks'] }),
  })
  const deleteMut = useMutation({
    mutationFn: (id: string) => client.deleteTask(create(DeleteTaskRequestSchema, { id })),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['tasks'] }); setSelected(null) },
  })

  const tasks = (q.data?.tasks ?? []).filter(t => status === 'all' || t.status === status)
  const total = q.data?.total ?? 0

  const statusColor = (s: string) =>
    s === 'completed' ? T.status.ok
  : s === 'failed'    ? T.status.error
  : s === 'running'   ? T.accent.runtime
                      : T.textDim

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <label className="text-[11px]" style={{ color: T.textDim }}>Status</label>
        <select
          value={status}
          onChange={e => setStatus(e.target.value as StatusFilter)}
          className="text-[12px] px-2 py-1 rounded bg-transparent border"
          style={{ color: T.text, borderColor: T.border }}
        >
          <option value="all">All</option>
          <option value="pending">Pending</option>
          <option value="running">Running</option>
          <option value="completed">Completed</option>
          <option value="failed">Failed</option>
        </select>
      </div>

      <MicroLabel>Tasks ({tasks.length})</MicroLabel>
      <Card padding="flush">
        <table className="w-full text-[12px] tabular-nums">
          <thead>
            <tr className="text-left" style={{ color: T.textDim }}>
              <th className="font-normal px-3 py-2">Prompt</th>
              <th className="font-normal px-3 py-2">Status</th>
              <th className="font-normal px-3 py-2 text-right">Iter.</th>
              <th className="font-normal px-3 py-2">Person</th>
              <th className="font-normal px-3 py-2">Schedule</th>
              <th className="font-normal px-3 py-2">Created</th>
            </tr>
          </thead>
          <tbody>
            {tasks.length === 0 && (
              <tr><td colSpan={6} className="px-3 py-4 text-center" style={{ color: T.textDim }}>No tasks match filter.</td></tr>
            )}
            {tasks.map(t => (
              <tr key={t.id} onClick={() => setSelected(t)}
                  className="border-t cursor-pointer hover:bg-white/[0.02]"
                  style={{ borderColor: T.border, color: T.text }}>
                <td className="px-3 py-2 truncate max-w-[420px]" style={{ color: T.textBright }}>
                  <span>{t.prompt}</span>
                  {t.profileName && t.profileName !== 'default' && (
                    <span
                      className="text-[10px] uppercase tracking-[0.1em] px-1.5 py-0.5 rounded ml-2"
                      style={{ background: T.surface, color: T.textDim }}
                    >
                      {t.profileName}
                    </span>
                  )}
                  {t.workspaceName && (
                    <span
                      className="text-[10px] uppercase tracking-[0.1em] px-1.5 py-0.5 rounded ml-2"
                      style={{ background: T.surface, color: T.textDim }}
                    >
                      ws: {t.workspaceName}
                    </span>
                  )}
                </td>
                <td className="px-3 py-2" style={{ color: statusColor(t.status) }}>{t.status}</td>
                <td className="px-3 py-2 text-right">{t.iterations}/{t.maxIterations || '—'}</td>
                <td className="px-3 py-2">{t.personName || '—'}</td>
                <td className="px-3 py-2">{t.schedule || '—'}</td>
                <td className="px-3 py-2">{t.createdAt}</td>
              </tr>
            ))}
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
        title={selected && <span className="text-[18px] font-semibold">Task · {selected.status}</span>}
      >
        {selected && (
          <div className="flex flex-col gap-4 text-[12px]" style={{ color: T.text }}>
            <Kv label="Prompt"><p className="whitespace-pre-wrap" style={{ color: T.textBright }}>{selected.prompt}</p></Kv>
            {selected.schedule && <Kv label="Schedule"><code className="font-mono text-[11px]">{selected.schedule}</code></Kv>}
            {selected.maxIterations > 0 && (
              <Kv label={`Iterations ${selected.iterations}/${selected.maxIterations}`}>
                <div className="h-1.5 rounded-full overflow-hidden" style={{ background: T.border }}>
                  <div className="h-full" style={{
                    width: `${Math.min(100, 100 * selected.iterations / selected.maxIterations)}%`,
                    background: T.accent.runtime,
                  }}/>
                </div>
              </Kv>
            )}
            {selected.personName && <Kv label="Person"><span>{selected.personName}</span></Kv>}
            {selected.summary && <Kv label="Summary"><p className="whitespace-pre-wrap">{selected.summary}</p></Kv>}
            <div className="flex gap-2 pt-2">
              {(selected.status === 'pending' || selected.status === 'running') && (
                <button onClick={() => { if (confirm('Cancel this task?')) cancelMut.mutate(selected.id) }}
                        className="text-[11px] px-3 py-1.5 rounded border"
                        style={{ color: T.accent.review, borderColor: `${T.accent.review}40` }}>
                  Cancel
                </button>
              )}
              <button onClick={() => { if (confirm('Delete this task?')) deleteMut.mutate(selected.id) }}
                      className="text-[11px] px-3 py-1.5 rounded border"
                      style={{ color: T.status.error, borderColor: `${T.status.error}40` }}>
                Delete
              </button>
            </div>
          </div>
        )}
      </Drawer>
    </div>
  )
}

function Kv({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-[10px] uppercase tracking-[0.12em] mb-1" style={{ color: T.textDim }}>{label}</div>
      {children}
    </div>
  )
}

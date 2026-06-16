import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListToolProposalsRequestSchema,
  ReviewToolProposalRequestSchema,
  ListPromptProposalsRequestSchema,
  ReviewPromptProposalRequestSchema,
  ListSensorProposalsRequestSchema,
  ReviewSensorProposalRequestSchema,
  ListSkillProposalsRequestSchema,
  ReviewSkillProposalRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client, prettyJSON } from '@/admin/shared'
import Drawer from '../Drawer'
import { T } from '../tokens'
import { Empty } from '../primitives'

type Kind = 'all' | 'tool' | 'prompt' | 'sensor' | 'skill'
type Status = 'pending' | 'approved' | 'rejected' | 'all'

type DrawerPayload =
  | { kind: 'tool';   id: string; title: string; body: React.ReactNode; onApprove: () => void; onReject: () => void }
  | { kind: 'prompt'; id: string; title: string; body: React.ReactNode; onApprove: () => void; onReject: () => void }
  | { kind: 'sensor'; id: string; title: string; body: React.ReactNode; onApprove: () => void; onReject: () => void }
  | { kind: 'skill';  id: string; title: string; body: React.ReactNode; onApprove: () => void; onReject: () => void }
  | null

export default function ProposalsView() {
  const qc = useQueryClient()
  const [kind, setKind] = useState<Kind>('all')
  const [status, setStatus] = useState<Status>('pending')
  const [payload, setPayload] = useState<DrawerPayload>(null)

  const toolQ   = useQuery({ queryKey: ['toolProposals'],    queryFn: () => client.listToolProposals(create(ListToolProposalsRequestSchema, {})) })
  const promptQ = useQuery({ queryKey: ['promptProposals'],  queryFn: () => client.listPromptProposals(create(ListPromptProposalsRequestSchema, {})) })
  const sensorQ = useQuery({ queryKey: ['sensor-proposals'], queryFn: () => client.listSensorProposals(create(ListSensorProposalsRequestSchema, {})) })
  const skillQ  = useQuery({ queryKey: ['skillProposals'],   queryFn: () => client.listSkillProposals(create(ListSkillProposalsRequestSchema, {})) })

  const reviewTool   = useMutation({
    mutationFn: (a: { id: string; approve: boolean }) => client.reviewToolProposal(create(ReviewToolProposalRequestSchema, a)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['toolProposals'] }); qc.invalidateQueries({ queryKey: ['toolProviders'] }); setPayload(null) },
  })
  const reviewPrompt = useMutation({
    mutationFn: (a: { id: string; approve: boolean }) => client.reviewPromptProposal(create(ReviewPromptProposalRequestSchema, a)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['promptProposals'] }); qc.invalidateQueries({ queryKey: ['prompts'] }); setPayload(null) },
  })
  const reviewSensor = useMutation({
    mutationFn: (a: { id: string; approve: boolean }) => client.reviewSensorProposal(create(ReviewSensorProposalRequestSchema, a)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['sensor-proposals'] }); setPayload(null) },
  })
  const reviewSkill = useMutation({
    mutationFn: (a: { id: string; approve: boolean }) => client.reviewSkillProposal(create(ReviewSkillProposalRequestSchema, a)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['skillProposals'] }); qc.invalidateQueries({ queryKey: ['skills'] }); setPayload(null) },
    onError: (err) => { window.alert(`Skill review failed: ${err instanceof Error ? err.message : String(err)}`) },
  })

  const keep = (s: string) => status === 'all' || s === status
  const tools   = (toolQ.data?.proposals   ?? []).filter(p => keep(p.status))
  const prompts = (promptQ.data?.proposals ?? []).filter(p => keep(p.status))
  const sensors = (sensorQ.data?.proposals ?? []).filter(p => keep(p.status))
  const skills  = (skillQ.data?.proposals  ?? []).filter(p => keep(p.status))

  const showTool   = kind === 'all' || kind === 'tool'
  const showPrompt = kind === 'all' || kind === 'prompt'
  const showSensor = kind === 'all' || kind === 'sensor'
  const showSkill  = kind === 'all' || kind === 'skill'

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center gap-2">
        <label className="text-[11px]" style={{ color: T.textDim }}>Kind</label>
        <select value={kind} onChange={e => setKind(e.target.value as Kind)}
                className="text-[12px] px-2 py-1 rounded bg-transparent border"
                style={{ color: T.text, borderColor: T.border }}>
          <option value="all">All</option>
          <option value="tool">Tool</option>
          <option value="prompt">Prompt</option>
          <option value="sensor">Sensor</option>
          <option value="skill">Skill</option>
        </select>

        <label className="text-[11px] ml-3" style={{ color: T.textDim }}>Status</label>
        <select value={status} onChange={e => setStatus(e.target.value as Status)}
                className="text-[12px] px-2 py-1 rounded bg-transparent border"
                style={{ color: T.text, borderColor: T.border }}>
          <option value="pending">Pending</option>
          <option value="approved">Approved</option>
          <option value="rejected">Rejected</option>
          <option value="all">All</option>
        </select>
      </div>

      {showTool && (
        <Section label={`Tool proposals (${tools.length})`}>
          {tools.length === 0 ? <Empty>No pending tool proposals.</Empty> : tools.map(p => (
            <Row
              key={p.id}
              badge={{ text: 'tool', color: '#a78bfa' }}
              title={p.toolName}
              subtitle={p.description}
              time={p.createdAt}
              status={p.status}
              onApprove={p.status === 'pending' ? () => reviewTool.mutate({ id: p.id, approve: true }) : undefined}
              onReject={p.status === 'pending' ? () => reviewTool.mutate({ id: p.id, approve: false }) : undefined}
              onOpen={() => setPayload({
                kind: 'tool', id: p.id, title: p.toolName,
                body: (
                  <div className="flex flex-col gap-3 text-[12px]" style={{ color: T.text }}>
                    <Kv label="Description"><p>{p.description}</p></Kv>
                    <Kv label="Rationale"><p className="italic">{p.rationale}</p></Kv>
                    <Kv label="MCP URL"><code className="font-mono text-[11px] break-all">{p.mcpUrl}</code></Kv>
                  </div>
                ),
                onApprove: () => reviewTool.mutate({ id: p.id, approve: true }),
                onReject:  () => reviewTool.mutate({ id: p.id, approve: false }),
              })}
            />
          ))}
        </Section>
      )}

      {showPrompt && (
        <Section label={`Prompt proposals (${prompts.length})`}>
          {prompts.length === 0 ? <Empty>No pending prompt proposals.</Empty> : prompts.map(p => (
            <Row
              key={p.id}
              badge={{ text: 'prompt', color: '#a78bfa' }}
              title="System prompt rewrite"
              subtitle={p.rationale}
              time={p.createdAt}
              status={p.status}
              onApprove={p.status === 'pending' ? () => reviewPrompt.mutate({ id: p.id, approve: true }) : undefined}
              onReject={p.status === 'pending' ? () => reviewPrompt.mutate({ id: p.id, approve: false }) : undefined}
              onOpen={() => setPayload({
                kind: 'prompt', id: p.id, title: 'Proposed system prompt',
                body: (
                  <div className="flex flex-col gap-3 text-[12px]" style={{ color: T.text }}>
                    <Kv label="Rationale"><p className="italic">{p.rationale}</p></Kv>
                    <Kv label="Proposed content">
                      <pre className="font-mono text-[11px] whitespace-pre-wrap p-3 rounded border" style={{ borderColor: T.border, background: 'rgba(0,0,0,0.3)' }}>{p.proposedContent}</pre>
                    </Kv>
                  </div>
                ),
                onApprove: () => reviewPrompt.mutate({ id: p.id, approve: true }),
                onReject:  () => reviewPrompt.mutate({ id: p.id, approve: false }),
              })}
            />
          ))}
        </Section>
      )}

      {showSensor && (
        <Section label={`Sensor proposals (${sensors.length})`}>
          {sensors.length === 0 ? <Empty>No pending sensor proposals.</Empty> : sensors.map(p => {
            const summary =
                p.kind === 'hass_state'       ? { primary: p.entityId || '(missing entity_id)', secondary: 'Home Assistant state change' }
              : p.kind === 'mcp_notification' ? { primary: `${p.toolName || '(missing provider)'} · ${p.entityId || '(missing method)'}`, secondary: 'MCP server push' }
              :                                 { primary: p.toolName || '(missing tool_name)', secondary: `every ${p.intervalSeconds}s` }
            return (
              <Row
                key={p.id}
                badge={{ text: p.kind || 'poll', color: '#38bdf8' }}
                title={summary.primary}
                subtitle={`${summary.secondary} — ${p.rationale}`}
                time={p.createdAt}
                status={p.status}
                onApprove={p.status === 'pending' ? () => reviewSensor.mutate({ id: p.id, approve: true }) : undefined}
                onReject={p.status === 'pending' ? () => reviewSensor.mutate({ id: p.id, approve: false }) : undefined}
                onOpen={() => setPayload({
                  kind: 'sensor', id: p.id, title: summary.primary,
                  body: (
                    <div className="flex flex-col gap-3 text-[12px]" style={{ color: T.text }}>
                      <Kv label="Kind"><span>{p.kind || 'poll'}</span></Kv>
                      <Kv label="Summary"><span>{summary.secondary}</span></Kv>
                      <Kv label="Rationale"><p className="italic">{p.rationale}</p></Kv>
                      {p.toolArgsJson && (
                        <Kv label="Args">
                          <pre className="font-mono text-[11px] whitespace-pre-wrap p-3 rounded border" style={{ borderColor: T.border, background: 'rgba(0,0,0,0.3)' }}>{prettyJSON(p.toolArgsJson)}</pre>
                        </Kv>
                      )}
                    </div>
                  ),
                  onApprove: () => reviewSensor.mutate({ id: p.id, approve: true }),
                  onReject:  () => reviewSensor.mutate({ id: p.id, approve: false }),
                })}
              />
            )
          })}
        </Section>
      )}

      {showSkill && (
        <Section label={`Skill proposals (${skills.length})`}>
          {skills.length === 0 ? <Empty>No pending skill proposals.</Empty> : skills.map(p => {
            const isEdit = !!p.targetSkillId
            return (
              <Row
                key={p.id}
                badge={{ text: isEdit ? 'skill edit' : 'skill', color: '#86efac' }}
                title={p.proposedName || '(unnamed skill)'}
                subtitle={p.proposedDescription || p.rationale}
                time={p.createdAt}
                status={p.status}
                onApprove={p.status === 'pending' ? () => reviewSkill.mutate({ id: p.id, approve: true }) : undefined}
                onReject={p.status === 'pending' ? () => reviewSkill.mutate({ id: p.id, approve: false }) : undefined}
                onOpen={() => setPayload({
                  kind: 'skill', id: p.id, title: p.proposedName || '(unnamed skill)',
                  body: (
                    <div className="flex flex-col gap-3 text-[12px]" style={{ color: T.text }}>
                      {isEdit && <Kv label="Editing skill"><code className="font-mono text-[11px]">{p.targetSkillId}</code></Kv>}
                      <Kv label="Description"><p>{p.proposedDescription}</p></Kv>
                      <Kv label="Binding"><code className="font-mono text-[11px]">{p.proposedBinding || '(none)'}</code></Kv>
                      <Kv label="Rationale"><p className="italic">{p.rationale}</p></Kv>
                      <Kv label="Markdown">
                        <pre className="font-mono text-[11px] whitespace-pre-wrap p-3 rounded border" style={{ borderColor: T.border, background: 'rgba(0,0,0,0.3)' }}>{p.proposedMarkdown}</pre>
                      </Kv>
                    </div>
                  ),
                  onApprove: () => reviewSkill.mutate({ id: p.id, approve: true }),
                  onReject:  () => reviewSkill.mutate({ id: p.id, approve: false }),
                })}
              />
            )
          })}
        </Section>
      )}

      <Drawer
        open={payload !== null}
        onClose={() => setPayload(null)}
        title={payload && <span className="text-[18px] font-semibold">{payload.title}</span>}
      >
        {payload && (
          <div className="flex flex-col gap-4">
            {payload.body}
            <div className="flex gap-2 pt-2">
              <button onClick={payload.onReject}
                      className="text-[11px] px-3 py-1.5 rounded border"
                      style={{ color: T.status.error, borderColor: `${T.status.error}40` }}>Reject</button>
              <button onClick={payload.onApprove}
                      className="text-[11px] px-3 py-1.5 rounded border font-semibold"
                      style={{ color: T.accent.review, borderColor: `${T.accent.review}66`, background: `${T.accent.review}14` }}>Approve</button>
            </div>
          </div>
        )}
      </Drawer>
    </div>
  )
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <div className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>{label}</div>
      {children}
    </section>
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

function Row({
  badge, title, subtitle, time, status, onOpen, onApprove, onReject,
}: {
  badge: { text: string; color: string }
  title: string
  subtitle?: string
  time: string
  status: string
  onOpen: () => void
  onApprove?: () => void
  onReject?: () => void
}) {
  return (
    <div onClick={onOpen}
         className="rounded-xl border p-3 flex items-start gap-3 cursor-pointer hover:bg-white/[0.02]"
         style={{ borderColor: T.border, background: T.raised }}>
      <span className="text-[10px] px-1.5 py-0.5 rounded uppercase tracking-wider shrink-0"
            style={{ background: `${badge.color}22`, color: badge.color }}>{badge.text}</span>
      <div className="flex-1 min-w-0">
        <div className="text-[13px]" style={{ color: T.textBright }}>{title}</div>
        {subtitle && <div className="text-[11px] mt-0.5 truncate" style={{ color: T.text }}>{subtitle}</div>}
      </div>
      <span className="text-[11px] tabular-nums shrink-0" style={{ color: T.textDim }}>{time}</span>
      <span className="text-[10px] px-1.5 py-0.5 rounded uppercase tracking-wider shrink-0"
            style={{
              background: status === 'pending' ? `${T.accent.review}22` : status === 'approved' ? `${T.status.ok}22` : `${T.status.error}22`,
              color:      status === 'pending' ? T.accent.review       : status === 'approved' ? T.status.ok       : T.status.error,
            }}>{status}</span>
      {onApprove && onReject && (
        <div className="flex gap-1 shrink-0" onClick={e => e.stopPropagation()}>
          <button onClick={onReject}  className="text-[11px] px-2 py-1 rounded border" style={{ color: T.status.error,  borderColor: `${T.status.error}40` }}>Reject</button>
          <button onClick={onApprove} className="text-[11px] px-2 py-1 rounded border" style={{ color: T.accent.review,  borderColor: `${T.accent.review}40` }}>Approve</button>
        </div>
      )}
    </div>
  )
}

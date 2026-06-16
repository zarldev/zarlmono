import { useQuery } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  GetAgentNameRequestSchema,
  ListPersonsRequestSchema,
  GetConversationLLMSettingsRequestSchema,
  GetTaskProviderSettingsRequestSchema,
  ListToolProvidersRequestSchema,
  ListRegisteredToolsRequestSchema,
  ListToolProposalsRequestSchema,
  ListPromptProposalsRequestSchema,
  ListSensorProposalsRequestSchema,
  ListToolCallsRequestSchema,
  ListSkillsRequestSchema,
  ListSkillProposalsRequestSchema,
  ListPromptTemplatesRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T, type ViewId } from './tokens'

type CardProps = { title: string; color: string; onClick: () => void; children: React.ReactNode }
function Card({ title, color, onClick, children }: CardProps) {
  return (
    <button
      onClick={onClick}
      className="text-left rounded-lg p-5 flex flex-col gap-3 hover:border-white/10"
      style={{
        background: T.raised,
        border: `1px solid ${T.border}`,
        transition: 'border-color 160ms ease-out',
      }}
    >
      <div className="flex items-center gap-2">
        <span
          className="w-1 h-1 rounded-full shrink-0"
          style={{ background: color, opacity: 0.8 }}
        />
        <span className="text-[9px] font-semibold uppercase tracking-[0.18em]" style={{ color: T.textDim }}>
          {title}
        </span>
      </div>
      {children}
    </button>
  )
}

function relativeTime(iso: string): string {
  if (!iso) return '—'
  const diff = (Date.now() - new Date(iso).getTime()) / 1000
  if (diff < 60)     return `${Math.floor(diff)}s ago`
  if (diff < 3600)   return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400)  return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

export default function Dashboard({ onOpen }: { onOpen: (v: ViewId) => void }) {
  const agent     = useQuery({ queryKey: ['agentName'],                 queryFn: () => client.getAgentName(create(GetAgentNameRequestSchema, {})) })
  const persons   = useQuery({ queryKey: ['persons'],                   queryFn: () => client.listPersons(create(ListPersonsRequestSchema, {})) })
  const convLLM   = useQuery({ queryKey: ['conversationLLMSettings'],   queryFn: () => client.getConversationLLMSettings(create(GetConversationLLMSettingsRequestSchema, {})) })
  const taskLLM   = useQuery({ queryKey: ['taskProviderSettings'],      queryFn: () => client.getTaskProviderSettings(create(GetTaskProviderSettingsRequestSchema, {})) })
  const providers = useQuery({ queryKey: ['toolProviders'],             queryFn: () => client.listToolProviders(create(ListToolProvidersRequestSchema, {})) })
  const regTools  = useQuery({ queryKey: ['registeredTools'],           queryFn: () => client.listRegisteredTools(create(ListRegisteredToolsRequestSchema, {})) })
  const toolProp  = useQuery({ queryKey: ['toolProposals'],             queryFn: () => client.listToolProposals(create(ListToolProposalsRequestSchema, {})) })
  const promptProp= useQuery({ queryKey: ['promptProposals'],           queryFn: () => client.listPromptProposals(create(ListPromptProposalsRequestSchema, {})) })
  const sensorProp= useQuery({ queryKey: ['sensor-proposals'],          queryFn: () => client.listSensorProposals(create(ListSensorProposalsRequestSchema, {})) })
  const calls     = useQuery({ queryKey: ['toolCalls', 0],              queryFn: () => client.listToolCalls(create(ListToolCallsRequestSchema, { limit: 5, offset: 0 })) })
  const skills    = useQuery({ queryKey: ['skills'],                    queryFn: () => client.listSkills(create(ListSkillsRequestSchema, {})) })
  const skillProp = useQuery({ queryKey: ['skillProposals'],            queryFn: () => client.listSkillProposals(create(ListSkillProposalsRequestSchema, {})) })
  const templates = useQuery({ queryKey: ['promptTemplates'],           queryFn: () => client.listPromptTemplates(create(ListPromptTemplatesRequestSchema, {})) })

  const pendTool   = (toolProp.data?.proposals   ?? []).filter(p => p.status === 'pending').length
  const pendPrompt = (promptProp.data?.proposals ?? []).filter(p => p.status === 'pending').length
  const pendSensor = (sensorProp.data?.proposals ?? []).filter(p => p.status === 'pending').length
  const pendSkill  = (skillProp.data?.proposals  ?? []).filter(p => p.status === 'pending').length
  const pendAny = pendTool + pendPrompt + pendSensor + pendSkill > 0

  const totalSkills    = skills.data?.skills?.length ?? 0
  const enabledSkills  = (skills.data?.skills ?? []).filter(s => s.enabled).length
  const totalTemplates = templates.data?.templates?.length ?? 0
  const editedTemplates = (templates.data?.templates ?? []).filter(t => t.hasOverride).length

  const recentCalls = (calls.data?.calls ?? []).slice(0, 5)
  const enabledProviders = (providers.data?.providers ?? []).filter(p => p.enabled).length

  return (
    <div className="grid grid-cols-2 gap-4 max-w-[900px]">
      <Card title="Identity" color={T.accent.identity} onClick={() => onOpen('identity')}>
        <div style={{ color: T.textBright }} className="text-[22px] font-semibold tabular-nums">{agent.data?.displayName || '—'}</div>
        <div className="flex gap-4 text-[12px]" style={{ color: T.text }}>
          <span>{persons.data?.persons?.length ?? 0} people</span>
        </div>
      </Card>

      <Card title="Runtime" color={T.accent.runtime} onClick={() => onOpen('models')}>
        <div className="flex flex-col gap-1 text-[12px]" style={{ color: T.text }}>
          <div><span style={{ color: T.textDim }}>conv</span> · <span style={{ color: T.textBright }}>{convLLM.data?.model || '—'}</span></div>
          <div><span style={{ color: T.textDim }}>task</span> · <span style={{ color: T.textBright }}>{taskLLM.data?.model || '—'}</span></div>
          <div><span style={{ color: T.textDim }}>providers</span> · <span style={{ color: T.textBright }}>{enabledProviders}</span> enabled</div>
          <div><span style={{ color: T.textDim }}>tools</span> · <span style={{ color: T.textBright }}>{regTools.data?.tools?.length ?? 0}</span> registered</div>
        </div>
      </Card>

      <Card title="Review queue" color={T.accent.review} onClick={() => onOpen('proposals')}>
        <div className="flex flex-wrap gap-x-6 gap-y-2 text-[28px] font-semibold tabular-nums"
             style={{ color: pendAny ? T.accent.review : T.textDim }}>
          <span>{pendTool}<span className="text-[11px] ml-1 font-normal" style={{ color: T.textDim }}>tool</span></span>
          <span>{pendPrompt}<span className="text-[11px] ml-1 font-normal" style={{ color: T.textDim }}>prompt</span></span>
          <span>{pendSensor}<span className="text-[11px] ml-1 font-normal" style={{ color: T.textDim }}>sensor</span></span>
          <span>{pendSkill}<span className="text-[11px] ml-1 font-normal" style={{ color: T.textDim }}>skill</span></span>
        </div>
      </Card>

      <Card title="Skills" color={T.accent.identity} onClick={() => onOpen('skills')}>
        <div style={{ color: T.textBright }} className="text-[22px] font-semibold tabular-nums">
          {enabledSkills}<span className="text-[13px] ml-1 font-normal" style={{ color: T.textDim }}>/ {totalSkills} enabled</span>
        </div>
        <div className="flex gap-4 text-[12px]" style={{ color: T.text }}>
          {pendSkill > 0 ? (
            <span style={{ color: T.accent.review }}>{pendSkill} pending proposal{pendSkill === 1 ? '' : 's'}</span>
          ) : totalSkills === 0 ? (
            <span style={{ color: T.textDim }}>No skills yet — procedures the agent follows.</span>
          ) : (
            <span style={{ color: T.textDim }}>No pending proposals.</span>
          )}
        </div>
      </Card>

      <Card title="Templates" color={T.accent.runtime} onClick={() => onOpen('templates')}>
        <div style={{ color: T.textBright }} className="text-[22px] font-semibold tabular-nums">
          {totalTemplates}<span className="text-[13px] ml-1 font-normal" style={{ color: T.textDim }}> registered</span>
        </div>
        <div className="flex gap-4 text-[12px]" style={{ color: T.text }}>
          {editedTemplates > 0 ? (
            <span style={{ color: T.accent.runtime }}>{editedTemplates} edited override{editedTemplates === 1 ? '' : 's'}</span>
          ) : (
            <span style={{ color: T.textDim }}>All on code defaults.</span>
          )}
        </div>
      </Card>

      <Card title="History" color={T.accent.history} onClick={() => onOpen('tool-calls')}>
        {recentCalls.length === 0 ? (
          <div className="text-[12px]" style={{ color: T.textDim }}>No tool calls yet.</div>
        ) : (
          <div className="flex flex-col gap-1.5">
            {recentCalls.map(c => {
              const ok = !c.error
              return (
                <div key={c.id} className="flex items-center gap-2 text-[12px]">
                  <span className="w-1.5 h-1.5 rounded-full shrink-0"
                        style={{ background: ok ? T.status.ok : T.status.error }} />
                  <span style={{ color: T.textBright }} className="truncate">{c.toolName}</span>
                  <span className="ml-auto tabular-nums" style={{ color: T.textDim }}>{relativeTime(c.createdAt)}</span>
                </div>
              )
            })}
          </div>
        )}
      </Card>
    </div>
  )
}

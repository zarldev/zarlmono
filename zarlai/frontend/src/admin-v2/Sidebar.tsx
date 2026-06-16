import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListToolProposalsRequestSchema,
  ListPromptProposalsRequestSchema,
  ListSensorProposalsRequestSchema,
  ListSkillProposalsRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T, type GroupId, type ViewId } from './tokens'

const COLLAPSE_KEY = 'zarl.admin.sidebar.collapsed'

type Item = { id: ViewId; label: string }
type Group = { id: GroupId; label: string; color: string; items: Item[] }

const GROUPS: Group[] = [
  { id: 'identity', label: 'Identity',     color: T.accent.identity, items: [
    { id: 'identity',  label: 'Identity' },
    { id: 'prompts',   label: 'Prompts' },
    { id: 'skills',    label: 'Skills' },
    { id: 'faces',     label: 'Faces' },
    { id: 'profiles',  label: 'Profiles' },
  ]},
  { id: 'runtime',  label: 'Runtime',      color: T.accent.runtime,  items: [
    { id: 'models',    label: 'Task runner' },
    { id: 'tools',     label: 'Tools' },
    { id: 'templates', label: 'Templates' },
    { id: 'tasks',     label: 'Tasks' },
  ]},
  { id: 'review',   label: 'Review queue', color: T.accent.review,   items: [
    { id: 'proposals', label: 'Proposals' },
  ]},
  { id: 'history',  label: 'History',      color: T.accent.history,  items: [
    { id: 'chats',      label: 'Chats' },
    { id: 'tool-calls', label: 'Tool calls' },
  ]},
]

function usePendingCounts() {
  // Poll every 5s. We only care about pending totals for the badge; we do not
  // need the full objects. React Query dedupes against any list view that
  // happens to be open and fetching the same data.
  const opts = { refetchInterval: 5000 }
  const tools   = useQuery({ ...opts, queryKey: ['toolProposals'],   queryFn: () => client.listToolProposals(create(ListToolProposalsRequestSchema, {})) })
  const prompts = useQuery({ ...opts, queryKey: ['promptProposals'], queryFn: () => client.listPromptProposals(create(ListPromptProposalsRequestSchema, {})) })
  const sensors = useQuery({ ...opts, queryKey: ['sensor-proposals'],queryFn: () => client.listSensorProposals(create(ListSensorProposalsRequestSchema, {})) })
  const skills  = useQuery({ ...opts, queryKey: ['skillProposals'],  queryFn: () => client.listSkillProposals(create(ListSkillProposalsRequestSchema, {})) })
  const count = (arr: { status: string }[] | undefined) => (arr ?? []).filter(p => p.status === 'pending').length
  return {
    tool:    count(tools.data?.proposals),
    prompt:  count(prompts.data?.proposals),
    sensor:  count(sensors.data?.proposals),
    skill:   count(skills.data?.proposals),
    get total() { return this.tool + this.prompt + this.sensor + this.skill },
  }
}

export default function Sidebar({
  active, onSelect, onHome,
}: {
  active: ViewId
  onSelect: (v: ViewId) => void
  onHome: () => void
}) {
  const [collapsed, setCollapsed] = useState<boolean>(() => localStorage.getItem(COLLAPSE_KEY) === '1')
  useEffect(() => { localStorage.setItem(COLLAPSE_KEY, collapsed ? '1' : '0') }, [collapsed])

  const pending = usePendingCounts()
  const reduceMotion = typeof window !== 'undefined'
    && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

  return (
    <aside
      className="shrink-0 flex flex-col border-r"
      style={{
        width: collapsed ? 56 : 240,
        background: T.surface,
        borderColor: T.border,
        transition: `width 180ms ease-out`,
      }}
    >
      <button
        onClick={onHome}
        className="flex items-center gap-2.5 px-4 py-4"
        style={{ color: T.textBright }}
      >
        <div className="w-6 h-6 rounded-md shrink-0" style={{ background: `linear-gradient(135deg, ${T.accent.identity}, transparent)` }} />
        {!collapsed && <span className="text-sm font-semibold">zarl admin</span>}
      </button>

      <nav className="flex-1 overflow-y-auto py-2">
        {GROUPS.map(group => {
          const badge = group.id === 'review' ? pending.total : 0
          return (
            <div key={group.id} className="mb-5">
              <div className="flex items-center gap-2.5 px-4 mb-2">
                <span
                  className="w-1 h-1 rounded-full shrink-0"
                  style={{ background: group.color, opacity: 0.8 }}
                />
                {!collapsed && (
                  <span className="text-[9px] font-semibold uppercase tracking-[0.18em]" style={{ color: T.textDim }}>
                    {group.label}
                  </span>
                )}
                {!collapsed && badge > 0 && (
                  <span
                    className="ml-auto text-[10px] px-1.5 py-0.5 rounded font-semibold"
                    style={{
                      background: `${group.color}22`,
                      color: group.color,
                      animation: reduceMotion ? undefined : 'pulse 2s ease-in-out infinite',
                    }}
                  >{badge}</span>
                )}
              </div>
              {group.items.map(item => {
                const isActive = active === item.id
                return (
                  <button
                    key={item.id}
                    onClick={() => onSelect(item.id)}
                    className="w-full flex items-center gap-2.5 pl-4 pr-3 py-1.5 text-left group"
                    style={{
                      color: isActive ? T.textBright : T.text,
                      transition: `color 160ms ease-out`,
                    }}
                  >
                    <span
                      className="w-1.5 h-1.5 rounded-full shrink-0"
                      style={{
                        background: isActive ? group.color : 'transparent',
                        boxShadow: isActive ? `0 0 8px ${group.color}80` : 'none',
                        transition: `background 160ms ease-out, box-shadow 160ms ease-out`,
                      }}
                    />
                    {!collapsed && <span className="text-[12px]">{item.label}</span>}
                  </button>
                )
              })}
            </div>
          )
        })}
      </nav>

      <button
        onClick={() => setCollapsed(c => !c)}
        title={collapsed ? 'Expand' : 'Collapse'}
        className="flex items-center justify-center h-10 border-t hover:text-white/80"
        style={{ color: T.textDim, borderColor: T.border, transition: 'color 160ms ease-out' }}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
             style={{ transform: collapsed ? 'rotate(180deg)' : 'none', transition: 'transform 200ms ease-out' }}>
          <polyline points="15 18 9 12 15 6" />
        </svg>
      </button>
    </aside>
  )
}

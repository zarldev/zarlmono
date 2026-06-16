import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListSkillsRequestSchema,
  CreateSkillRequestSchema,
  UpdateSkillRequestSchema,
  DeleteSkillRequestSchema,
  ListSkillProposalsRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T, type ViewId } from '../tokens'
import {
  Card, MicroLabel, Field, Empty,
  PrimaryButton, SecondaryButton, DangerButton,
  fieldCls, fieldStyle,
} from '../primitives'

type Skill = {
  id: string
  name: string
  description: string
  markdown: string
  profileBinding: string
  enabled: boolean
  createdAt: string
  updatedAt: string
}

const BINDINGS = ['', 'default', 'researcher', 'coder'] as const
const BINDING_LABELS: Record<string, string> = {
  '':           'Global (all profiles)',
  'default':    'Default (live chat)',
  'researcher': 'Researcher',
  'coder':      'Coder',
}

// Two-pane CRUD: left rail lists skills with selection chrome; right pane
// shows detail + edit form for the selected skill, or a create form when
// "+ New skill" is active. Matches the Prompts / Profiles / Faces pattern.

export default function SkillsView({ onOpen }: { onOpen: (v: ViewId) => void }) {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [mode, setMode] = useState<'view' | 'edit' | 'create'>('view')
  const [draft, setDraft] = useState<Partial<Skill>>({})

  const qc = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['skills'],
    queryFn: () => client.listSkills(create(ListSkillsRequestSchema, {})),
  })

  const proposalsQ = useQuery({
    queryKey: ['skillProposals'],
    queryFn: () => client.listSkillProposals(create(ListSkillProposalsRequestSchema, {})),
  })
  const pendingProposals = (proposalsQ.data?.proposals ?? []).filter((p: { status: string }) => p.status === 'pending').length

  const createMut = useMutation({
    mutationFn: (s: Partial<Skill>) =>
      client.createSkill(create(CreateSkillRequestSchema, {
        name: s.name ?? '',
        description: s.description ?? '',
        markdown: s.markdown ?? '',
        profileBinding: s.profileBinding ?? '',
        enabled: s.enabled ?? true,
      })),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['skills'] })
      setMode('view')
      setDraft({})
      // Select the newly-created skill so the right pane lands on it.
      if (r.skill?.id) setSelectedId(r.skill.id)
    },
  })

  const updateMut = useMutation({
    mutationFn: (s: Skill) =>
      client.updateSkill(create(UpdateSkillRequestSchema, {
        id: s.id,
        name: s.name,
        description: s.description,
        markdown: s.markdown,
        profileBinding: s.profileBinding,
        enabled: s.enabled,
      })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['skills'] })
      setMode('view')
      setDraft({})
    },
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) => client.deleteSkill(create(DeleteSkillRequestSchema, { id })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['skills'] })
      setSelectedId(null)
      setMode('view')
    },
  })

  const skills: Skill[] = (data?.skills ?? []) as Skill[]

  // Auto-select the first skill on first load so the right pane isn't
  // staring at the user blank.
  useEffect(() => {
    if (mode === 'create') return
    if (!selectedId && skills.length > 0) setSelectedId(skills[0].id)
  }, [skills, selectedId, mode])

  const selected = skills.find(s => s.id === selectedId) ?? null

  if (isLoading) {
    return (
      <Card>
        <MicroLabel>Skills</MicroLabel>
        <Empty>Loading…</Empty>
      </Card>
    )
  }

  function startNew() {
    setSelectedId(null)
    setMode('create')
    setDraft({ enabled: true })
  }

  function startEdit(s: Skill) {
    setMode('edit')
    setDraft(s)
  }

  function cancelEdit() {
    setMode('view')
    setDraft({})
  }

  function pickSkill(id: string) {
    setSelectedId(id)
    setMode('view')
    setDraft({})
  }

  return (
    <div className="grid grid-cols-[280px_1fr] gap-4 min-h-[600px]">
      <aside className="flex flex-col gap-2 min-w-0">
        <div className="flex items-center justify-between gap-2">
          <MicroLabel>Skills ({skills.length})</MicroLabel>
          <PrimaryButton group="identity" onClick={startNew}>+ New</PrimaryButton>
        </div>
        {pendingProposals > 0 && (
          <PrimaryButton group="review" onClick={() => onOpen('proposals')} title="Review pending skill proposals">
            {pendingProposals} pending review →
          </PrimaryButton>
        )}
        <div className="flex flex-col gap-1 overflow-y-auto">
          {skills.length === 0 && (
            <Empty>No skills yet.</Empty>
          )}
          {skills.map(s => (
            <SkillRow
              key={s.id}
              skill={s}
              selected={s.id === selectedId && mode !== 'create'}
              onClick={() => pickSkill(s.id)}
            />
          ))}
        </div>
      </aside>

      {mode === 'create' ? (
        <SkillForm
          draft={draft}
          setDraft={setDraft}
          onSave={() => createMut.mutate(draft)}
          onCancel={() => { setMode('view'); setDraft({}) }}
          pending={createMut.isPending}
          saveLabel="Create"
          title="New skill"
        />
      ) : selected ? (
        mode === 'edit' ? (
          <SkillForm
            draft={draft}
            setDraft={setDraft}
            onSave={() => updateMut.mutate({ ...selected, ...draft } as Skill)}
            onCancel={cancelEdit}
            pending={updateMut.isPending}
            saveLabel="Save"
            title={`Editing ${selected.name}`}
          />
        ) : (
          <SkillDetail
            skill={selected}
            onEdit={() => startEdit(selected)}
            onToggleEnabled={() => updateMut.mutate({ ...selected, enabled: !selected.enabled })}
            onDelete={() => { if (confirm(`Delete skill "${selected.name}"?`)) deleteMut.mutate(selected.id) }}
            pending={updateMut.isPending || deleteMut.isPending}
          />
        )
      ) : (
        <Card>
          <Empty>
            No skills yet. Skills are procedural guides injected into the system prompt
            when the user's message matches. Capabilities — not facts (those go to memory).
          </Empty>
        </Card>
      )}
    </div>
  )
}

function SkillRow({ skill, selected, onClick }: {
  skill: Skill
  selected: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full text-left rounded-lg transition-colors flex flex-col gap-1 px-3 py-2"
      style={{
        borderLeft: `2px solid ${selected ? T.accent.identity : 'transparent'}`,
        background: selected ? `${T.accent.identity}14` : 'transparent',
        opacity: skill.enabled ? 1 : 0.5,
      }}
    >
      <span
        className="text-[12px] font-mono font-semibold truncate"
        style={{ color: selected ? T.textBright : T.text }}
      >
        {skill.name}
      </span>
      <div className="flex items-center gap-2">
        <span
          className="text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider shrink-0"
          style={{ background: `${T.accent.identity}22`, color: T.accent.identity }}
        >
          {BINDING_LABELS[skill.profileBinding] ?? skill.profileBinding}
        </span>
        {!skill.enabled && (
          <span
            className="text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider shrink-0"
            style={{ background: `${T.status.error}22`, color: T.status.error }}
          >
            disabled
          </span>
        )}
      </div>
    </button>
  )
}

function SkillDetail({ skill, onEdit, onToggleEnabled, onDelete, pending }: {
  skill: Skill
  onEdit: () => void
  onToggleEnabled: () => void
  onDelete: () => void
  pending: boolean
}) {
  return (
    <div className="flex flex-col gap-4 min-w-0">
      <Card>
        <div className="flex items-start justify-between gap-3">
          <div className="flex flex-col gap-2 min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-[15px] font-mono font-semibold" style={{ color: T.textBright }}>
                {skill.name}
              </span>
              <span
                className="text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider"
                style={{ background: `${T.accent.identity}22`, color: T.accent.identity }}
              >
                {BINDING_LABELS[skill.profileBinding] ?? skill.profileBinding}
              </span>
              {!skill.enabled && (
                <span
                  className="text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider"
                  style={{ background: `${T.status.error}22`, color: T.status.error }}
                >
                  disabled
                </span>
              )}
            </div>
            <p className="text-[13px]" style={{ color: T.text }}>
              {skill.description || <span style={{ color: T.textDim }}>(no description)</span>}
            </p>
          </div>
        </div>
        <div className="flex items-center justify-between gap-2 pt-2 border-t" style={{ borderColor: T.border }}>
          <DangerButton onClick={onDelete} disabled={pending}>Delete</DangerButton>
          <div className="flex items-center gap-2">
            <SecondaryButton onClick={onToggleEnabled} disabled={pending}>
              {skill.enabled ? 'Disable' : 'Enable'}
            </SecondaryButton>
            <PrimaryButton group="identity" onClick={onEdit} disabled={pending}>
              Edit
            </PrimaryButton>
          </div>
        </div>
      </Card>

      <Card>
        <MicroLabel>Procedure</MicroLabel>
        <pre className="text-[12px] leading-relaxed whitespace-pre-wrap font-mono" style={{ color: T.text }}>
{skill.markdown}
        </pre>
      </Card>
    </div>
  )
}

function SkillForm({
  draft, setDraft, onSave, onCancel, pending, saveLabel, title,
}: {
  draft: Partial<Skill>
  setDraft: (s: Partial<Skill>) => void
  onSave: () => void
  onCancel: () => void
  pending: boolean
  saveLabel: string
  title: string
}) {
  const disabled = !draft.name?.trim() || !draft.description?.trim() || !draft.markdown?.trim()
  return (
    <div className="flex flex-col gap-4 min-w-0">
      <Card>
        <MicroLabel>{title}</MicroLabel>
        <Field label="Name (snake_case)">
          <input
            value={draft.name ?? ''}
            onChange={e => setDraft({ ...draft, name: e.target.value })}
            placeholder="research_report_format"
            className={`${fieldCls} font-mono`}
            style={fieldStyle}
            autoFocus
          />
        </Field>
        <Field label="Description (when this should fire)">
          <input
            value={draft.description ?? ''}
            onChange={e => setDraft({ ...draft, description: e.target.value })}
            placeholder="When producing a comparison research report"
            className={fieldCls}
            style={fieldStyle}
          />
        </Field>
        <Field label="Binding">
          <select
            value={draft.profileBinding ?? ''}
            onChange={e => setDraft({ ...draft, profileBinding: e.target.value })}
            className={fieldCls}
            style={{ ...fieldStyle, background: T.surface }}
          >
            {BINDINGS.map(b => <option key={b} value={b}>{BINDING_LABELS[b]}</option>)}
          </select>
        </Field>
        <label className="flex items-center gap-2 text-[12px]" style={{ color: T.text }}>
          <input
            type="checkbox"
            checked={draft.enabled ?? true}
            onChange={e => setDraft({ ...draft, enabled: e.target.checked })}
          />
          Enabled
        </label>
      </Card>

      <Card>
        <MicroLabel>Procedure (markdown body)</MicroLabel>
        <textarea
          value={draft.markdown ?? ''}
          onChange={e => setDraft({ ...draft, markdown: e.target.value })}
          rows={14}
          placeholder={'# Research report format\n\n- Lead with a 1-sentence summary\n- Then 3-5 labeled findings with source URLs\n- End with suggested next steps'}
          className="px-3 py-2 rounded-lg border bg-transparent text-[12px] outline-none leading-relaxed font-mono"
          style={{ color: T.textBright, borderColor: T.border, resize: 'vertical' }}
        />
        <div className="flex gap-2 justify-end pt-2 border-t" style={{ borderColor: T.border }}>
          <SecondaryButton onClick={onCancel}>Cancel</SecondaryButton>
          <PrimaryButton group="identity" onClick={onSave} disabled={disabled || pending}>
            {pending ? 'Saving…' : saveLabel}
          </PrimaryButton>
        </div>
      </Card>
    </div>
  )
}

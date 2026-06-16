import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListPromptTemplatesRequestSchema,
  UpdatePromptTemplateRequestSchema,
  ResetPromptTemplateRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T } from '../tokens'
import {
  Card, MicroLabel, Empty,
  PrimaryButton, SecondaryButton, DangerButton,
} from '../primitives'

type Template = {
  key: string
  content: string
  defaultContent: string
  hasOverride: boolean
  updatedAt: string
}

// Two-pane layout mirroring Skills / Prompts / Profiles: left rail lists
// template keys with override state; right pane shows the selected
// template's current content (editable) and its code default (collapsed).

export default function TemplatesView() {
  const qc = useQueryClient()
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const [draft, setDraft] = useState<string>('')
  const [dirty, setDirty] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['promptTemplates'],
    queryFn: () => client.listPromptTemplates(create(ListPromptTemplatesRequestSchema, {})),
  })

  const updateMut = useMutation({
    mutationFn: (args: { key: string; content: string }) =>
      client.updatePromptTemplate(create(UpdatePromptTemplateRequestSchema, args)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['promptTemplates'] })
      setDirty(false)
    },
  })

  const resetMut = useMutation({
    mutationFn: (key: string) =>
      client.resetPromptTemplate(create(ResetPromptTemplateRequestSchema, { key })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['promptTemplates'] })
      setDirty(false)
    },
  })

  const templates: Template[] = (data?.templates ?? []) as Template[]

  useEffect(() => {
    if (!selectedKey && templates.length > 0) setSelectedKey(templates[0].key)
  }, [templates, selectedKey])

  const selected = templates.find(t => t.key === selectedKey) ?? null

  // Sync draft to selected template on selection change.
  useEffect(() => {
    if (selected) {
      setDraft(selected.content)
      setDirty(false)
    }
  }, [selected?.key])

  if (isLoading) {
    return (
      <Card>
        <MicroLabel>Templates</MicroLabel>
        <Empty>Loading…</Empty>
      </Card>
    )
  }

  if (templates.length === 0) {
    return (
      <Card>
        <MicroLabel>Templates</MicroLabel>
        <Empty>
          No templates registered. Code owners call RegisterDefault at startup
          to seed editable templates here.
        </Empty>
      </Card>
    )
  }

  return (
    <div className="grid grid-cols-[280px_1fr] gap-4 min-h-[600px]">
      <aside className="flex flex-col gap-2 min-w-0">
        <MicroLabel>Prompt templates ({templates.length})</MicroLabel>
        <p className="text-[11px]" style={{ color: T.textDim }}>
          Operator-editable strings. <code style={{ color: T.textBright }}>{'{{placeholders}}'}</code> substitute at render time.
        </p>
        <div className="flex flex-col gap-1 overflow-y-auto">
          {templates.map(t => (
            <TemplateRow
              key={t.key}
              template={t}
              selected={t.key === selectedKey}
              onClick={() => setSelectedKey(t.key)}
            />
          ))}
        </div>
      </aside>

      {selected ? (
        <TemplateDetail
          template={selected}
          draft={draft}
          setDraft={v => { setDraft(v); setDirty(v !== selected.content) }}
          dirty={dirty}
          onSave={() => updateMut.mutate({ key: selected.key, content: draft })}
          onRevertDraft={() => { setDraft(selected.content); setDirty(false) }}
          onReset={() => { if (confirm('Reset to code default? This drops your override.')) resetMut.mutate(selected.key) }}
          saving={updateMut.isPending}
          resetting={resetMut.isPending}
        />
      ) : (
        <Card>
          <Empty>Select a template to view and edit.</Empty>
        </Card>
      )}
    </div>
  )
}

function TemplateRow({ template, selected, onClick }: {
  template: Template
  selected: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full text-left rounded-lg transition-colors flex flex-col gap-1 px-3 py-2"
      style={{
        borderLeft: `2px solid ${selected ? T.accent.runtime : 'transparent'}`,
        background: selected ? `${T.accent.runtime}14` : 'transparent',
      }}
    >
      <div className="flex items-center gap-2">
        <span
          className="text-[12px] font-mono font-semibold truncate"
          style={{ color: selected ? T.textBright : T.text }}
        >
          {template.key}
        </span>
        {template.hasOverride && (
          <span
            className="text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider shrink-0"
            style={{ background: `${T.accent.runtime}22`, color: T.accent.runtime }}
          >
            edited
          </span>
        )}
      </div>
      <span className="text-[11px] truncate" style={{ color: T.textDim }}>
        {template.content.split('\n')[0].slice(0, 80)}
      </span>
    </button>
  )
}

function TemplateDetail({
  template, draft, setDraft, dirty, onSave, onRevertDraft, onReset, saving, resetting,
}: {
  template: Template
  draft: string
  setDraft: (v: string) => void
  dirty: boolean
  onSave: () => void
  onRevertDraft: () => void
  onReset: () => void
  saving: boolean
  resetting: boolean
}) {
  return (
    <div className="flex flex-col gap-4 min-w-0">
      <Card>
        <div className="flex items-start justify-between gap-3">
          <div className="flex flex-col gap-2 min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-[15px] font-mono font-semibold" style={{ color: T.textBright }}>
                {template.key}
              </span>
              {template.hasOverride && (
                <span
                  className="text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider"
                  style={{ background: `${T.accent.runtime}22`, color: T.accent.runtime }}
                >
                  edited
                </span>
              )}
            </div>
            <p className="text-[11px]" style={{ color: T.textDim }}>
              {template.hasOverride
                ? 'An operator override is active. Reset to fall back to the code default.'
                : 'Matches the code default. Edit to create an override.'}
            </p>
          </div>
        </div>
      </Card>

      <Card>
        <MicroLabel>Content</MicroLabel>
        <textarea
          value={draft}
          onChange={e => setDraft(e.target.value)}
          rows={Math.max(8, draft.split('\n').length + 1)}
          className="px-3 py-2 rounded-lg border bg-transparent text-[12px] outline-none leading-relaxed font-mono"
          style={{ color: T.textBright, borderColor: T.border, resize: 'vertical' }}
        />
        <div className="flex items-center justify-between gap-2 pt-2 border-t" style={{ borderColor: T.border }}>
          <div>
            {template.hasOverride && (
              <DangerButton onClick={onReset} disabled={resetting}>
                {resetting ? 'Resetting…' : 'Reset to default'}
              </DangerButton>
            )}
          </div>
          <div className="flex items-center gap-2">
            {dirty && (
              <SecondaryButton onClick={onRevertDraft} disabled={saving}>Discard</SecondaryButton>
            )}
            <PrimaryButton
              group="runtime"
              onClick={onSave}
              disabled={!dirty || saving || draft.trim() === ''}
            >
              {saving ? 'Saving…' : 'Save'}
            </PrimaryButton>
          </div>
        </div>
      </Card>

      {template.hasOverride && (
        <Card>
          <MicroLabel>Code default</MicroLabel>
          <pre
            className="text-[12px] leading-relaxed whitespace-pre-wrap font-mono"
            style={{ color: T.textDim }}
          >
{template.defaultContent}
          </pre>
        </Card>
      )}
    </div>
  )
}

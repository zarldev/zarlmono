import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListPromptsRequestSchema,
  CreatePromptRequestSchema,
  UpdatePromptRequestSchema,
  SetActivePromptRequestSchema,
  DeletePromptRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T } from '../tokens'
import Markdown from '../Markdown'
import { MicroLabel, PrimaryButton, SecondaryButton, DangerButton } from '../primitives'

type Mode = 'preview' | 'edit'

// Template placeholders the runtime substitutes via service.RenderSystemPrompt
// (see service/prompt.go). Keep this list in sync with that function — a
// dedicated RPC would be overkill for three stable values.
const TEMPLATES: { token: string; description: string }[] = [
  { token: '{{agent_name}}',  description: "agent's spoken name (default Zarl)" },
  { token: '{{person_name}}', description: "identified user; falls back to 'the current user'" },
  { token: '{{location}}',    description: "user's coordinates; 'unknown' if absent" },
]

export default function PromptsView() {
  const qc = useQueryClient()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [mode, setMode] = useState<Mode>('preview')
  const [draft, setDraft] = useState('')
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const editTextareaRef = useRef<HTMLTextAreaElement | null>(null)
  const newTextareaRef  = useRef<HTMLTextAreaElement | null>(null)

  // Insert a token at the textarea's cursor; if no textarea is focused, append.
  function insertTemplate(token: string) {
    const ta = creating ? newTextareaRef.current : editTextareaRef.current
    if (!ta) {
      setDraft(d => d + token)
      return
    }
    const start = ta.selectionStart ?? ta.value.length
    const end   = ta.selectionEnd   ?? ta.value.length
    const next  = ta.value.slice(0, start) + token + ta.value.slice(end)
    setDraft(next)
    requestAnimationFrame(() => {
      ta.focus()
      const pos = start + token.length
      ta.setSelectionRange(pos, pos)
    })
  }

  const { data, isLoading } = useQuery({
    queryKey: ['prompts'],
    queryFn: () => client.listPrompts(create(ListPromptsRequestSchema, {})),
  })
  // Active prompt floats to the top; the rest keep server order.
  const prompts = (() => {
    const all = data?.prompts ?? []
    const active = all.filter(p => p.active)
    const rest   = all.filter(p => !p.active)
    return [...active, ...rest]
  })()
  const selected = prompts.find(p => p.id === selectedId) ?? null

  useEffect(() => {
    if (selected) setDraft(selected.content)
  }, [selected?.id])

  useEffect(() => {
    if (!selectedId && !creating && prompts.length > 0) setSelectedId(prompts[0].id)
  }, [prompts, selectedId, creating])

  const createMut = useMutation({
    mutationFn: (args: { name: string; content: string }) =>
      client.createPrompt(create(CreatePromptRequestSchema, args)),
    onSuccess: (res) => {
      qc.invalidateQueries({ queryKey: ['prompts'] })
      setCreating(false)
      setNewName('')
      setDraft('')
      if (res?.prompt) setSelectedId(res.prompt.id)
    },
  })
  const updateMut = useMutation({
    mutationFn: (args: { id: string; content: string }) =>
      client.updatePrompt(create(UpdatePromptRequestSchema, args)),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['prompts'] }); setMode('preview') },
  })
  const activateMut = useMutation({
    mutationFn: (id: string) => client.setActivePrompt(create(SetActivePromptRequestSchema, { id })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['prompts'] }),
  })
  const deleteMut = useMutation({
    mutationFn: (id: string) => client.deletePrompt(create(DeletePromptRequestSchema, { id })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['prompts'] })
      setSelectedId(null)
    },
  })

  if (isLoading) return <p className="text-sm" style={{ color: T.textDim }}>Loading…</p>

  const startNew = () => {
    setCreating(true)
    setSelectedId(null)
    setMode('edit')
    setNewName('')
    setDraft('')
  }

  const dirty = mode === 'edit' && !creating && selected && draft !== selected.content

  return (
    <div className="grid grid-cols-[280px_1fr] gap-4 min-h-[600px]">
      {/* List pane */}
      <aside className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <MicroLabel>{prompts.length} saved</MicroLabel>
          <PrimaryButton group="identity" onClick={startNew}>+ New</PrimaryButton>
        </div>

        <div className="flex flex-col gap-1.5 overflow-y-auto">
          {prompts.length === 0 && !creating && (
            <p className="text-[12px] px-2 py-3" style={{ color: T.textDim }}>No prompts yet.</p>
          )}
          {prompts.map(p => {
            const isSelected = p.id === selectedId
            const firstLine = p.content.split('\n').find(l => l.trim() !== '') ?? ''
            return (
              <button
                key={p.id}
                onClick={() => { setSelectedId(p.id); setCreating(false); setMode('preview') }}
                className="text-left rounded-lg p-2.5 border"
                style={{
                  background: isSelected ? `${T.accent.identity}10` : T.raised,
                  borderColor: isSelected ? `${T.accent.identity}40` : T.border,
                  borderLeft: `2px solid ${isSelected ? T.accent.identity : 'transparent'}`,
                }}
              >
                <div className="flex items-center gap-2">
                  <span
                    className="w-1.5 h-1.5 rounded-full shrink-0"
                    style={{ background: p.active ? T.accent.review : T.border }}
                    aria-label={p.active ? 'active' : 'inactive'}
                  />
                  <span className="text-[13px] font-medium flex-1 truncate" style={{ color: T.textBright }}>
                    {p.name}
                  </span>
                  {p.active && (
                    <span
                      className="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded shrink-0"
                      style={{ background: `${T.accent.review}22`, color: T.accent.review }}
                    >active</span>
                  )}
                </div>
                <p className="text-[11px] mt-1 truncate" style={{ color: T.textDim }}>
                  {firstLine || '(empty)'}
                </p>
              </button>
            )
          })}
        </div>
      </aside>

      {/* Detail pane */}
      <section
        className="flex flex-col rounded-xl border overflow-hidden"
        style={{ background: T.raised, borderColor: T.border }}
      >
        {creating ? (
          <NewPromptPane
            name={newName}
            onNameChange={setNewName}
            content={draft}
            onContentChange={setDraft}
            onCreate={() => createMut.mutate({ name: newName.trim(), content: draft })}
            onCancel={() => { setCreating(false); setNewName(''); setDraft('') }}
            pending={createMut.isPending}
            textareaRef={newTextareaRef}
            onInsertTemplate={insertTemplate}
          />
        ) : selected ? (
          <>
            <header
              className="flex items-center gap-3 px-5 py-3 border-b"
              style={{ borderColor: T.border }}
            >
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <h2 className="text-[15px] font-semibold truncate" style={{ color: T.textBright }}>
                    {selected.name}
                  </h2>
                  {selected.active && (
                    <span
                      className="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded"
                      style={{ background: `${T.accent.review}22`, color: T.accent.review }}
                    >active</span>
                  )}
                </div>
              </div>
              <div
                className="flex rounded border overflow-hidden text-[11px]"
                style={{ borderColor: T.border }}
              >
                {(['preview', 'edit'] as Mode[]).map(m => (
                  <button
                    key={m}
                    onClick={() => setMode(m)}
                    className="px-3 py-1 capitalize"
                    style={{
                      background: mode === m ? `${T.accent.identity}18` : 'transparent',
                      color: mode === m ? T.accent.identity : T.textDim,
                    }}
                  >{m}</button>
                ))}
              </div>
            </header>

            <div className="flex-1 overflow-y-auto">
              {mode === 'preview' ? (
                <div className="px-6 py-5 max-w-[75ch]">
                  <Markdown source={selected.content} />
                </div>
              ) : (
                <textarea
                  ref={editTextareaRef}
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  className="block w-full h-full min-h-[420px] px-6 py-5 bg-transparent font-mono text-[12px] leading-relaxed resize-none outline-none"
                  style={{ color: T.text }}
                />
              )}
            </div>

            <TemplateBar mode={mode} onInsert={insertTemplate} />

            <footer
              className="flex items-center gap-2 px-5 py-3 border-t"
              style={{ borderColor: T.border }}
            >
              {!selected.active && (
                <PrimaryButton
                  group="identity"
                  onClick={() => activateMut.mutate(selected.id)}
                  disabled={activateMut.isPending}
                >
                  {activateMut.isPending ? 'Activating…' : 'Activate'}
                </PrimaryButton>
              )}
              {mode === 'edit' && (
                <PrimaryButton
                  group="identity"
                  onClick={() => updateMut.mutate({ id: selected.id, content: draft })}
                  disabled={!dirty || updateMut.isPending}
                >
                  {updateMut.isPending ? 'Saving…' : 'Save changes'}
                </PrimaryButton>
              )}
              <div className="flex-1" />
              <DangerButton
                onClick={() => { if (confirm(`Delete "${selected.name}"?`)) deleteMut.mutate(selected.id) }}
              >
                Delete
              </DangerButton>
            </footer>
          </>
        ) : (
          <div className="flex-1 flex items-center justify-center p-10">
            <p className="text-[12px]" style={{ color: T.textDim }}>
              Select a prompt on the left, or create a new one.
            </p>
          </div>
        )}
      </section>
    </div>
  )
}

function NewPromptPane({
  name, onNameChange, content, onContentChange, onCreate, onCancel, pending,
  textareaRef, onInsertTemplate,
}: {
  name: string
  onNameChange: (v: string) => void
  content: string
  onContentChange: (v: string) => void
  onCreate: () => void
  onCancel: () => void
  pending: boolean
  textareaRef: React.RefObject<HTMLTextAreaElement | null>
  onInsertTemplate: (token: string) => void
}) {
  const canCreate = name.trim() !== '' && content.trim() !== '' && !pending
  return (
    <>
      <header className="px-5 py-3 border-b flex items-center gap-3" style={{ borderColor: T.border }}>
        <span className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>New prompt</span>
        <input
          autoFocus
          type="text"
          value={name}
          onChange={(e) => onNameChange(e.target.value)}
          placeholder="Name (e.g. default v9)"
          className="flex-1 bg-transparent text-[14px] font-semibold outline-none"
          style={{ color: T.textBright }}
        />
      </header>
      <div className="flex-1">
        <textarea
          ref={textareaRef}
          value={content}
          onChange={(e) => onContentChange(e.target.value)}
          placeholder="# System prompt&#10;&#10;Write in Markdown — headings, lists, emphasis are rendered in Preview.&#10;&#10;Click a template chip below to insert a placeholder at the cursor."
          className="block w-full h-full min-h-[420px] px-6 py-5 bg-transparent font-mono text-[12px] leading-relaxed resize-none outline-none"
          style={{ color: T.text }}
        />
      </div>
      <TemplateBar mode="edit" onInsert={onInsertTemplate} />
      <footer className="flex items-center gap-2 px-5 py-3 border-t" style={{ borderColor: T.border }}>
        <PrimaryButton group="identity" onClick={onCreate} disabled={!canCreate}>
          {pending ? 'Creating…' : 'Create'}
        </PrimaryButton>
        <SecondaryButton onClick={onCancel}>Cancel</SecondaryButton>
      </footer>
    </>
  )
}

function TemplateBar({ mode, onInsert }: { mode: Mode; onInsert: (token: string) => void }) {
  const editable = mode === 'edit'
  return (
    <div
      className="flex items-center gap-2 px-5 py-2 border-t flex-wrap"
      style={{ borderColor: T.border, background: 'rgba(255,255,255,0.015)' }}
    >
      <span className="text-[10px] uppercase tracking-[0.12em] mr-1" style={{ color: T.textDim }}>
        Template values
      </span>
      {TEMPLATES.map(t => (
        <button
          key={t.token}
          type="button"
          onClick={() => editable && onInsert(t.token)}
          disabled={!editable}
          title={editable ? `Insert ${t.token} — ${t.description}` : `${t.description} (switch to Edit to insert)`}
          className="text-[11px] font-mono px-2 py-0.5 rounded border transition-colors disabled:cursor-default"
          style={{
            color: editable ? T.accent.identity : T.textDim,
            borderColor: editable ? `${T.accent.identity}40` : T.border,
            background: editable ? `${T.accent.identity}0a` : 'transparent',
          }}
        >
          {t.token}
        </button>
      ))}
      <span className="text-[10px] ml-auto" style={{ color: T.textDim }}>
        {editable ? 'click to insert at cursor' : 'switch to Edit to insert'}
      </span>
    </div>
  )
}


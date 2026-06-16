import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListPersonsRequestSchema,
  UpdatePersonRequestSchema,
  DeletePersonRequestSchema,
  ListPersonMemoriesRequestSchema,
  DeletePersonMemoryRequestSchema,
  type PersonMsg,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T } from '../tokens'
import {
  Card, MicroLabel, Field, Empty,
  PrimaryButton, SecondaryButton, DangerButton,
  fieldCls, fieldStyle,
} from '../primitives'

function relativeTime(iso: string): string {
  if (!iso) return '—'
  const diff = (Date.now() - new Date(iso).getTime()) / 1000
  if (diff < 60)    return `${Math.floor(diff)}s ago`
  if (diff < 3600)  return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  return `${Math.floor(diff / 86400)}d ago`
}

// Avatar — shared photo/initial render. Size is the pixel edge; caller
// decides whether to scale. Photo is optional; initial fallback tints with
// the identity accent for visual continuity with the group colour.
function Avatar({ person, size }: { person: { photo: string; name: string }; size: number }) {
  if (person.photo) {
    return (
      <img
        src={`data:image/jpeg;base64,${person.photo}`}
        alt={person.name}
        className="rounded-full object-cover border shrink-0"
        style={{ width: size, height: size, borderColor: `${T.accent.identity}30` }}
      />
    )
  }
  return (
    <div
      className="rounded-full flex items-center justify-center font-semibold border shrink-0"
      style={{
        width: size,
        height: size,
        background: `${T.accent.identity}18`,
        borderColor: `${T.accent.identity}30`,
        color: T.accent.identity,
        fontSize: size * 0.4,
      }}
    >
      {person.name[0]?.toUpperCase() ?? '?'}
    </div>
  )
}

export default function FacesView() {
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['persons'],
    queryFn: () => client.listPersons(create(ListPersonsRequestSchema, {})),
  })

  const persons = data?.persons ?? []

  // Auto-select the first person on first load so the detail pane isn't
  // blank staring at the user.
  useEffect(() => {
    if (!selectedId && persons.length > 0) setSelectedId(persons[0].id)
  }, [persons, selectedId])

  // If the selected id is no longer in the list (deleted, or list not yet
  // loaded), fall back to the first available one.
  const selected = persons.find(p => p.id === selectedId) ?? null

  if (isLoading) {
    return <p className="text-[13px]" style={{ color: T.textDim }}>Loading…</p>
  }

  if (persons.length === 0) {
    return (
      <Card>
        <MicroLabel>Faces</MicroLabel>
        <p className="text-[13px]" style={{ color: T.text }}>
          No faces enrolled yet.
        </p>
        <Empty>
          Speak to the assistant on camera to enroll, or run the onboarding wizard
          at <code style={{ color: T.accent.identity }}>/onboard</code> for a guided setup
          with three-pose capture.
        </Empty>
      </Card>
    )
  }

  return (
    <div className="grid grid-cols-[280px_1fr] gap-4 min-h-[600px]">
      <aside className="flex flex-col gap-2 min-w-0">
        <div className="flex items-center justify-between">
          <MicroLabel>
            {persons.length} known {persons.length === 1 ? 'face' : 'faces'}
          </MicroLabel>
        </div>
        <div className="flex flex-col gap-1 overflow-y-auto">
          {persons.map(p => (
            <FaceRow
              key={p.id}
              person={p}
              selected={p.id === selectedId}
              onClick={() => setSelectedId(p.id)}
            />
          ))}
        </div>
      </aside>

      {selected ? (
        <DetailPane key={selected.id} person={selected} onDeleted={() => setSelectedId(null)} />
      ) : (
        <Card>
          <Empty>Select a face to view details.</Empty>
        </Card>
      )}
    </div>
  )
}

function FaceRow({ person, selected, onClick }: {
  person: PersonMsg
  selected: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full text-left rounded-lg transition-colors flex items-center gap-2.5 px-2 py-2"
      style={{
        borderLeft: `2px solid ${selected ? T.accent.identity : 'transparent'}`,
        background: selected ? `${T.accent.identity}14` : 'transparent',
      }}
    >
      <Avatar person={person} size={32} />
      <div className="flex flex-col min-w-0 flex-1">
        <span
          className="text-[13px] font-medium truncate"
          style={{ color: selected ? T.textBright : T.text }}
        >
          {person.name}
        </span>
        {person.notes && (
          <span className="text-[11px] truncate" style={{ color: T.textDim }}>
            {person.notes.split('\n')[0]}
          </span>
        )}
      </div>
    </button>
  )
}

function DetailPane({ person, onDeleted }: {
  person: PersonMsg
  onDeleted: () => void
}) {
  const qc = useQueryClient()
  const [name, setName] = useState(person.name)
  const [notes, setNotes] = useState(person.notes)
  const [memQuery, setMemQuery] = useState('')

  // Re-sync fields when selection changes (keyed at the parent level so the
  // component remounts, but belt-and-braces for prop drift).
  useEffect(() => {
    setName(person.name)
    setNotes(person.notes)
  }, [person.id])

  const updateMut = useMutation({
    mutationFn: (args: { id: string; name: string; notes: string }) =>
      client.updatePerson(create(UpdatePersonRequestSchema, args)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['persons'] }),
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) =>
      client.deletePerson(create(DeletePersonRequestSchema, { id })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['persons'] })
      onDeleted()
    },
  })

  const memQ = useQuery({
    queryKey: ['personMemories', person.name],
    queryFn: () =>
      client.listPersonMemories(
        create(ListPersonMemoriesRequestSchema, { personName: person.name })
      ),
  })

  const deleteMemMut = useMutation({
    mutationFn: (id: string) =>
      client.deletePersonMemory(create(DeletePersonMemoryRequestSchema, { id })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['personMemories', person.name] }),
  })

  const dirty = name.trim() !== person.name || notes !== person.notes
  const canSave = dirty && name.trim() !== ''

  const allMemories = memQ.data?.memories ?? []
  const q = memQuery.trim().toLowerCase()
  const filtered = q === '' ? allMemories : allMemories.filter(m => m.fact.toLowerCase().includes(q))

  return (
    <div className="flex flex-col gap-4 min-w-0">
      <Card>
        <div className="flex items-start gap-4">
          <Avatar person={person} size={96} />
          <div className="flex flex-col gap-3 flex-1 min-w-0">
            <Field label="Name">
              <input
                value={name}
                onChange={e => setName(e.target.value)}
                className={fieldCls}
                style={fieldStyle}
              />
            </Field>
            <Field label="Notes">
              <textarea
                value={notes}
                onChange={e => setNotes(e.target.value)}
                placeholder="Add notes about this person…"
                className={`${fieldCls} resize-y`}
                style={{ ...fieldStyle, minHeight: 96 }}
              />
            </Field>
          </div>
        </div>
        <div className="flex items-center justify-between gap-2 pt-2 border-t" style={{ borderColor: T.border }}>
          <DangerButton
            onClick={() => { if (confirm(`Delete "${person.name}"?`)) deleteMut.mutate(person.id) }}
            disabled={deleteMut.isPending}
          >
            {deleteMut.isPending ? 'Deleting…' : 'Delete face'}
          </DangerButton>
          <div className="flex items-center gap-2">
            {dirty && (
              <SecondaryButton onClick={() => { setName(person.name); setNotes(person.notes) }}>
                Reset
              </SecondaryButton>
            )}
            <PrimaryButton
              group="identity"
              onClick={() => updateMut.mutate({ id: person.id, name: name.trim(), notes })}
              disabled={!canSave || updateMut.isPending}
            >
              {updateMut.isPending ? 'Saving…' : 'Save'}
            </PrimaryButton>
          </div>
        </div>
      </Card>

      <Card>
        <div className="flex items-center gap-3">
          <MicroLabel>
            Memories ({q === '' ? allMemories.length : `${filtered.length} / ${allMemories.length}`})
          </MicroLabel>
          {allMemories.length > 0 && (
            <input
              type="text"
              value={memQuery}
              onChange={e => setMemQuery(e.target.value)}
              placeholder="Filter memories…"
              className="ml-auto px-2.5 py-1 rounded-md border bg-transparent text-[12px] outline-none w-[220px]"
              style={{ color: T.textBright, borderColor: T.border }}
            />
          )}
        </div>

        {memQ.isLoading ? (
          <Empty>Loading…</Empty>
        ) : allMemories.length === 0 ? (
          <Empty>No memories stored about {person.name}.</Empty>
        ) : filtered.length === 0 ? (
          <Empty>No memories match "{memQuery}".</Empty>
        ) : (
          <div className="flex flex-col gap-1.5">
            {filtered.map(m => (
              <div
                key={m.id}
                className="rounded-lg border px-3 py-2 flex flex-col gap-1"
                style={{ borderColor: T.border, background: T.surface }}
              >
                <p className="text-[13px] leading-snug" style={{ color: T.text }}>{m.fact}</p>
                <div className="flex items-center justify-between">
                  <span className="text-[11px]" style={{ color: T.textDim }}>
                    {relativeTime(m.createdAt)}
                  </span>
                  <button
                    onClick={() => { if (confirm('Delete this memory?')) deleteMemMut.mutate(m.id) }}
                    disabled={deleteMemMut.isPending}
                    className="text-[11px] disabled:opacity-40"
                    style={{ color: T.status.error }}
                  >
                    Delete
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}

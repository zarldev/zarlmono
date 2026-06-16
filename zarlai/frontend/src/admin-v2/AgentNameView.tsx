import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import { GetAgentNameRequestSchema, SetAgentNameRequestSchema } from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T } from './tokens'

export default function AgentNameView() {
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['agentName'], queryFn: () => client.getAgentName(create(GetAgentNameRequestSchema, {})) })
  const [draft, setDraft] = useState('')
  useEffect(() => { if (q.data?.displayName) setDraft(q.data.displayName) }, [q.data?.displayName])
  const mut = useMutation({
    mutationFn: (displayName: string) => client.setAgentName(create(SetAgentNameRequestSchema, { displayName })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['agentName'] }),
  })
  const trimmed = draft.trim()
  const dirty = trimmed !== '' && trimmed !== q.data?.displayName

  return (
    <div className="flex flex-col gap-3 max-w-md">
      <p className="text-[12px]" style={{ color: T.textDim }}>
        Spoken name. Applies to new sessions.
      </p>
      <div className="flex items-center gap-3">
        <input
          type="text"
          value={draft}
          onChange={e => setDraft(e.target.value)}
          placeholder="Zarl"
          className="px-3 py-1.5 rounded border bg-transparent text-[13px] w-[220px]"
          style={{ color: T.textBright, borderColor: T.border }}
        />
        <button
          onClick={() => mut.mutate(trimmed)}
          disabled={!dirty || mut.isPending}
          className="text-[11px] px-3 py-1.5 rounded border transition-colors disabled:opacity-40"
          style={{ color: T.accent.identity, borderColor: `${T.accent.identity}40` }}
        >
          {mut.isPending ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  )
}

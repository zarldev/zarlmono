import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListProfilesRequestSchema,
  UpsertProfileOverrideRequestSchema,
  DeleteProfileOverrideRequestSchema,
  ProfileOverrideSchema,
  GetConversationLLMSettingsRequestSchema,
  GetTaskProviderSettingsRequestSchema,
  ListAvailableModelsRequestSchema,
  ListRegisteredToolsRequestSchema,
  type ProfileInfo,
  type AvailableModelMsg,
  type RegisteredToolMsg,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T } from '../tokens'
import Markdown from '../Markdown'
import { MicroLabel, PrimaryButton, SecondaryButton, GhostButton } from '../primitives'

export default function ProfilesView() {
  const [selectedName, setSelectedName] = useState<string | null>(null)

  const { data, isLoading, error } = useQuery({
    queryKey: ['profiles'],
    queryFn: () => client.listProfiles(create(ListProfilesRequestSchema, {})),
  })
  const convQ = useQuery({
    queryKey: ['conversationLLMSettings'],
    queryFn: () => client.getConversationLLMSettings(create(GetConversationLLMSettingsRequestSchema, {})),
  })
  const taskQ = useQuery({
    queryKey: ['taskProviderSettings'],
    queryFn: () => client.getTaskProviderSettings(create(GetTaskProviderSettingsRequestSchema, {})),
  })
  const availableQ = useQuery({
    queryKey: ['availableModels'],
    queryFn: () => client.listAvailableModels(create(ListAvailableModelsRequestSchema, {})),
  })
  const toolsQ = useQuery({
    queryKey: ['registeredTools'],
    queryFn: () => client.listRegisteredTools(create(ListRegisteredToolsRequestSchema, {})),
  })

  const profiles = data?.profiles ?? []
  const discovered = (availableQ.data?.models ?? []).map(m => m.name)
  const configured = [convQ.data?.model, taskQ.data?.model]
    .filter((m): m is string => !!m && m.trim() !== '')
  const modelChoices = Array.from(new Set([...configured, ...discovered]))
  const allTools = toolsQ.data?.tools ?? []

  // Auto-select first profile
  useEffect(() => {
    if (!selectedName && profiles.length > 0) setSelectedName(profiles[0].name)
  }, [profiles, selectedName])

  const selected = profiles.find(p => p.name === selectedName) ?? null

  if (isLoading) return <p className="text-[13px]" style={{ color: T.textDim }}>Loading…</p>
  if (error) return <p className="text-[13px]" style={{ color: T.status.error }}>Error: {(error as Error).message}</p>

  return (
    <div className="grid grid-cols-[280px_1fr] gap-4 min-h-[600px]">
      {/* List pane */}
      <aside className="flex flex-col gap-2">
        <MicroLabel className="tabular-nums">
          {profiles.length} {profiles.length === 1 ? 'profile' : 'profiles'}
        </MicroLabel>

        <div className="flex flex-col gap-1.5 overflow-y-auto">
          {profiles.length === 0 && (
            <p className="text-[12px] px-2 py-3" style={{ color: T.textDim }}>No profiles found.</p>
          )}
          {profiles.map(p => {
            const isSelected = p.name === selectedName
            const toolCount = p.toolNames.length
            const hasOverride = overrideIsActive(p)
            return (
              <button
                key={p.name}
                onClick={() => setSelectedName(p.name)}
                className="text-left rounded-lg p-2.5 border"
                style={{
                  background: isSelected ? `${T.accent.identity}10` : T.raised,
                  borderColor: isSelected ? `${T.accent.identity}40` : T.border,
                  borderLeft: `2px solid ${isSelected ? T.accent.identity : 'transparent'}`,
                }}
              >
                <div className="flex items-center gap-2">
                  <span className="text-[13px] font-medium flex-1 truncate" style={{ color: T.textBright }}>
                    {p.name}
                  </span>
                  {hasOverride && (
                    <span
                      className="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded shrink-0"
                      style={{ background: `${T.accent.identity}22`, color: T.accent.identity }}
                    >
                      override
                    </span>
                  )}
                </div>
                <p className="text-[11px] mt-0.5 tabular-nums" style={{ color: T.textDim }}>
                  {toolCount > 0 ? `${toolCount} tools` : '(dynamic)'}
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
        {selected ? (
          <ProfileDetail
            profile={selected}
            modelChoices={modelChoices}
            availableModels={availableQ.data?.models ?? []}
            allTools={allTools}
          />
        ) : (
          <div className="flex-1 flex items-center justify-center p-10">
            <p className="text-[12px]" style={{ color: T.textDim }}>
              Select a profile on the left.
            </p>
          </div>
        )}
      </section>
    </div>
  )
}

// overrideIsActive returns true when the profile has any active override knob.
function overrideIsActive(p: ProfileInfo): boolean {
  if (!p.override) return false
  return (
    p.override.model !== undefined ||
    p.override.promptPrefix !== undefined ||
    p.override.maxIterations !== undefined ||
    p.override.toolNames.length > 0
  )
}

function ProfileDetail({
  profile, modelChoices, availableModels, allTools,
}: {
  profile: ProfileInfo
  modelChoices: string[]
  availableModels: AvailableModelMsg[]
  allTools: RegisteredToolMsg[]
}) {
  const qc = useQueryClient()

  // Effective displayed value = override ?? default (for model/maxIter that have clear defaults)
  const [model, setModel] = useState(profile.override?.model ?? '')
  const [promptPrefix, setPromptPrefix] = useState(
    profile.override?.promptPrefix ?? profile.defaultPromptPrefix
  )
  const [prefixMode, setPrefixMode] = useState<'edit' | 'preview'>('edit')
  const [maxIter, setMaxIter] = useState<number | ''>(profile.override?.maxIterations ?? '')

  // Tools customization
  const [toolCustomize, setToolCustomize] = useState(false)
  // Selected tool names when in customize mode
  const [selectedTools, setSelectedTools] = useState<string[]>([])

  const [msg, setMsg] = useState<string | null>(null)

  // Sync state when profile selection changes
  useEffect(() => {
    setModel(profile.override?.model ?? '')
    setPromptPrefix(profile.override?.promptPrefix ?? profile.defaultPromptPrefix)
    setPrefixMode('edit')
    setMaxIter(profile.override?.maxIterations ?? '')
    setMsg(null)
    // If the override already has tool_names, enter customize mode with those names pre-selected
    if (profile.override?.toolNames && profile.override.toolNames.length > 0) {
      setToolCustomize(true)
      setSelectedTools(profile.override.toolNames)
    } else {
      setToolCustomize(false)
      setSelectedTools([])
    }
  }, [profile.name])

  const upsert = useMutation({
    mutationFn: (toolNames: string[]) => client.upsertProfileOverride(create(UpsertProfileOverrideRequestSchema, {
      profileName: profile.name,
      override: create(ProfileOverrideSchema, {
        model: model === '' ? undefined : model,
        promptPrefix: promptPrefix === profile.defaultPromptPrefix ? undefined : (promptPrefix === '' ? undefined : promptPrefix),
        maxIterations: maxIter === '' ? undefined : Number(maxIter),
        toolNames,
      }),
    })),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['profiles'] }); setMsg('Saved') },
    onError: (e: Error) => setMsg(`Error: ${e.message}`),
  })

  const reset = useMutation({
    mutationFn: () => client.deleteProfileOverride(create(DeleteProfileOverrideRequestSchema, {
      profileName: profile.name,
    })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['profiles'] })
      setModel('')
      setPromptPrefix(profile.defaultPromptPrefix)
      setMaxIter('')
      setToolCustomize(false)
      setSelectedTools([])
      setMsg('Cleared')
    },
    onError: (e: Error) => setMsg(`Error: ${e.message}`),
  })

  const handleSave = () => {
    upsert.mutate(toolCustomize ? selectedTools : [])
  }

  const handleRestoreDefault = () => {
    setPromptPrefix(profile.defaultPromptPrefix)
    upsert.mutate(toolCustomize ? selectedTools : [])
  }

  const handleEnterCustomize = () => {
    setToolCustomize(true)
    // Default to profile's baked-in whitelist if non-empty, else all tools
    if (profile.toolNames.length > 0) {
      setSelectedTools(profile.toolNames)
    } else {
      setSelectedTools(allTools.map(t => t.name))
    }
  }

  const handleExitCustomize = () => {
    setToolCustomize(false)
    setSelectedTools([])
    // Save immediately with no tool override
    upsert.mutate([])
  }

  const toggleTool = (name: string) => {
    setSelectedTools(prev =>
      prev.includes(name) ? prev.filter(n => n !== name) : [...prev, name]
    )
  }

  // Group tools by provider; 'native'/'built-in'/'' sorts first
  const toolGroups = groupToolsByProvider(allTools)

  return (
    <>
      {/* Header */}
      <header
        className="flex items-center gap-3 px-5 py-3 border-b"
        style={{ borderColor: T.border }}
      >
        <h2 className="text-[15px] font-semibold flex-1 truncate" style={{ color: T.textBright }}>
          {profile.name}
        </h2>
        {overrideIsActive(profile) && (
          <span
            className="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded"
            style={{ background: `${T.accent.identity}22`, color: T.accent.identity }}
          >
            override active
          </span>
        )}
      </header>

      {/* Body */}
      <div className="flex-1 overflow-y-auto px-5 py-5 flex flex-col gap-5">
        {/* Configuration section — single flat form */}
        <section className="flex flex-col gap-4">
          <div className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>
            Configuration
          </div>

          {/* Model */}
          <label className="flex flex-col gap-1.5">
            <span className="text-[11px]" style={{ color: T.textDim }}>Model</span>
            <select
              value={model}
              onChange={e => setModel(e.target.value)}
              className="px-2.5 py-1.5 rounded-lg border text-[12px] outline-none"
              style={{
                background: T.surface,
                borderColor: T.border,
                color: T.text,
              }}
            >
              <option value="">(use default{profile.defaultModel ? `: ${profile.defaultModel}` : ''})</option>
              {modelChoices.map(m => {
                const found = availableModels.find(x => x.name === m)
                const label = found
                  ? `${m} · ${found.provider}${found.size ? ` · ${found.size}` : ''}`
                  : m
                return <option key={m} value={m}>{label}</option>
              })}
              {model !== '' && !modelChoices.includes(model) && (
                <option value={model}>{model} (legacy)</option>
              )}
            </select>
          </label>

          {/* Prompt prefix */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <span className="text-[11px]" style={{ color: T.textDim }}>Prompt prefix</span>
              <span className="text-[10px]" style={{ color: T.textDim }}>(Markdown supported)</span>
              <div className="ml-auto flex rounded border overflow-hidden text-[11px]"
                   style={{ borderColor: T.border }}>
                {(['edit', 'preview'] as const).map(m => (
                  <button
                    key={m}
                    type="button"
                    onClick={() => setPrefixMode(m)}
                    className="px-3 py-0.5 capitalize"
                    style={{
                      background: prefixMode === m ? `${T.accent.identity}18` : 'transparent',
                      color: prefixMode === m ? T.accent.identity : T.textDim,
                    }}
                  >{m}</button>
                ))}
              </div>
            </div>
            {prefixMode === 'edit' ? (
              <textarea
                value={promptPrefix}
                onChange={e => setPromptPrefix(e.target.value)}
                placeholder="(none)&#10;&#10;Markdown — headings, lists, code, emphasis. Rendered into the system prompt for this profile."
                className="px-3 py-2 rounded-lg border font-mono text-[12px] leading-relaxed outline-none resize-y"
                style={{
                  background: T.surface,
                  borderColor: T.border,
                  color: T.text,
                  minHeight: 160,
                }}
              />
            ) : (
              <div
                className="rounded-lg border px-4 py-3 overflow-y-auto"
                style={{
                  background: T.surface,
                  borderColor: T.border,
                  minHeight: 160,
                  maxHeight: 480,
                }}
              >
                {promptPrefix.trim() === '' ? (
                  <p className="text-[12px] italic" style={{ color: T.textDim }}>(empty — switch to Edit to add)</p>
                ) : (
                  <Markdown source={promptPrefix} />
                )}
              </div>
            )}
            {profile.defaultPromptPrefix && promptPrefix !== profile.defaultPromptPrefix && (
              <GhostButton type="button" onClick={handleRestoreDefault} className="self-start">
                Restore default
              </GhostButton>
            )}
          </div>

          {/* Max iterations */}
          <label className="flex flex-col gap-1.5">
            <span className="text-[11px]" style={{ color: T.textDim }}>
              Max iterations
            </span>
            <input
              type="number"
              value={maxIter}
              onChange={e => setMaxIter(e.target.value === '' ? '' : Number(e.target.value))}
              min={1}
              max={profile.defaultMaxIterations}
              placeholder={`(use default: ${profile.defaultMaxIterations})`}
              className="px-2.5 py-1.5 rounded-lg border text-[12px] outline-none"
              style={{
                background: T.surface,
                borderColor: T.border,
                color: T.text,
              }}
            />
          </label>

          {/* Tools */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center gap-2">
              <span className="text-[11px]" style={{ color: T.textDim }}>Tools</span>
              {toolCustomize ? (
                <GhostButton type="button" onClick={handleExitCustomize} className="ml-auto">
                  Restore default
                </GhostButton>
              ) : (
                <SecondaryButton type="button" onClick={handleEnterCustomize} className="ml-auto">
                  Customize
                </SecondaryButton>
              )}
            </div>

            {toolCustomize ? (
              <div
                className="rounded-lg border p-3 flex flex-col gap-3"
                style={{ background: T.surface, borderColor: T.border }}
              >
                {toolGroups.map(({ provider, tools }) => (
                  <div key={provider} className="flex flex-col gap-1">
                    <span
                      className="text-[10px] uppercase tracking-wider"
                      style={{ color: T.textDim }}
                    >
                      {provider || 'native'}
                    </span>
                    {tools.map(t => (
                      <label
                        key={t.name}
                        className="flex items-center gap-2 cursor-pointer py-0.5"
                      >
                        <input
                          type="checkbox"
                          checked={selectedTools.includes(t.name)}
                          onChange={() => toggleTool(t.name)}
                          className="rounded"
                          style={{ accentColor: T.accent.identity }}
                        />
                        <span
                          className="font-mono text-[12px]"
                          style={{ color: T.text }}
                        >
                          {t.name}
                        </span>
                      </label>
                    ))}
                  </div>
                ))}
                {toolGroups.length === 0 && (
                  <p className="text-[11px] italic" style={{ color: T.textDim }}>
                    No tools registered.
                  </p>
                )}
              </div>
            ) : (
              <p className="text-[11px] italic" style={{ color: T.textDim }}>
                {profile.toolNames.length > 0
                  ? `${profile.toolNames.length} baked-in tools — customize to override`
                  : 'Dynamic (all registered tools) — customize to restrict'}
              </p>
            )}
          </div>
        </section>
      </div>

      {/* Footer */}
      <footer
        className="flex items-center gap-2 px-5 py-3 border-t"
        style={{ borderColor: T.border }}
      >
        <PrimaryButton group="identity" onClick={handleSave} disabled={upsert.isPending}>
          {upsert.isPending ? 'Saving…' : 'Save'}
        </PrimaryButton>
        <SecondaryButton onClick={() => reset.mutate()} disabled={reset.isPending}>
          {reset.isPending ? 'Clearing…' : 'Reset all'}
        </SecondaryButton>
        {msg && (
          <span className="text-[11px]" style={{ color: T.textDim }}>{msg}</span>
        )}
      </footer>
    </>
  )
}

type ToolGroup = { provider: string; tools: RegisteredToolMsg[] }

// groupToolsByProvider sorts groups alphabetically with native/'' tools first.
function groupToolsByProvider(tools: RegisteredToolMsg[]): ToolGroup[] {
  const map = new Map<string, RegisteredToolMsg[]>()
  for (const t of tools) {
    const key = t.provider || ''
    const arr = map.get(key) ?? []
    arr.push(t)
    map.set(key, arr)
  }
  const keys = Array.from(map.keys()).sort((a, b) => {
    if (a === '' && b !== '') return -1
    if (b === '' && a !== '') return 1
    return a.localeCompare(b)
  })
  return keys.map(k => ({ provider: k, tools: map.get(k)! }))
}

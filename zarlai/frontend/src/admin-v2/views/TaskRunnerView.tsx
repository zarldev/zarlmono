import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  GetTaskProviderSettingsRequestSchema,
  UpdateTaskProviderSettingsRequestSchema,
  ListAvailableModelsRequestSchema,
  ListWorkspacesRequestSchema,
  UpsertWorkspaceRequestSchema,
  DeleteWorkspaceRequestSchema,
  type WorkspaceInfo,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T } from '../tokens'
import { Card, MicroLabel, PrimaryButton, SecondaryButton, GhostButton, DangerButton } from '../primitives'

const inputCls = 'px-3 py-1.5 rounded-lg border bg-transparent text-[13px] outline-none w-full'
const inputStyle = { color: T.textBright, borderColor: T.border }
const fieldStyle = { color: T.textBright, borderColor: T.border, background: T.surface }

const PROVIDERS = ['llamacpp', 'ollama', 'openai', 'anthropic'] as const

// ── Task runner LLM form ─────────────────────────────────────────────────────

function TaskRunnerForm() {
  const qc = useQueryClient()

  const { data: settings, isLoading } = useQuery({
    queryKey: ['taskProviderSettings'],
    queryFn: () => client.getTaskProviderSettings(create(GetTaskProviderSettingsRequestSchema, {})),
  })
  const { data: available } = useQuery({
    queryKey: ['availableModels'],
    queryFn: () => client.listAvailableModels(create(ListAvailableModelsRequestSchema, {})),
  })

  const [provider, setProvider] = useState('ollama')
  const [model, setModel] = useState('')
  const [customModel, setCustomModel] = useState(false)
  const [baseUrl, setBaseUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [contextBudget, setContextBudget] = useState(40000)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [initialized, setInitialized] = useState(false)

  if (settings && !initialized) {
    setProvider(settings.provider || 'ollama')
    setModel(settings.model || '')
    setBaseUrl(settings.baseUrl || '')
    setContextBudget(settings.contextBudget || 40000)
    setInitialized(true)
  }

  // Models are discovered per-provider: the backend probes /api/tags for
  // ollama and /v1/models for llamacpp and tags each result with the
  // provider it came from. Filter on the currently-selected provider so
  // the dropdown reflects what's actually installed there. OpenAI /
  // Anthropic don't have a probe — their model field is free-text.
  const discoveredModels = (available?.models ?? []).filter(m => m.provider === provider)
  // Include the currently-saved model only when its provider matches the
  // selected one — otherwise a provider switch leaves the stale model
  // (e.g. a Claude name) contaminating the llamacpp dropdown.
  const savedModelMatchesProvider = settings?.provider === provider && !!settings?.model
  const modelChoices = Array.from(new Set([
    ...(savedModelMatchesProvider ? [settings!.model] : []),
    ...discoveredModels.map(m => m.name),
  ]))

  const saveMut = useMutation({
    mutationFn: () => client.updateTaskProviderSettings(create(UpdateTaskProviderSettingsRequestSchema, {
      provider,
      model,
      baseUrl,
      apiKey,
      contextBudget,
    })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['taskProviderSettings'] })
      setApiKey('')
      setInitialized(false)
    },
  })

  if (isLoading) return null

  const showApiKey = provider === 'openai' || provider === 'anthropic'

  // The currently saved provider may not be in the PROVIDERS const if the DB
  // holds an unexpected value — include it so the select never goes blank.
  const providerChoices = Array.from(new Set([
    ...(settings?.provider ? [settings.provider] : []),
    ...PROVIDERS,
  ]))

  const PROVIDER_LABELS: Record<string, string> = {
    llamacpp:  'llama.cpp (local)',
    ollama:    'Ollama (local)',
    openai:    'OpenAI',
    anthropic: 'Anthropic',
  }

  return (
    <div className="flex flex-col gap-3">
      {/* Provider */}
      <label className="flex flex-col gap-1.5">
        <span className="text-[11px]" style={{ color: T.textDim }}>Provider</span>
        <select
          value={provider}
          onChange={e => {
            const next = e.target.value
            setProvider(next)
            setModel('')
            setCustomModel(false)
            // Prefill a sensible default base URL when switching to a
            // provider that requires one and the field is currently
            // empty or holds a different provider's URL. Saves the user
            // from typing "http://localhost:8081/v1" every time.
            const defaults: Record<string, string> = {
              llamacpp: 'http://localhost:8081/v1',
              ollama:   'http://localhost:11434',
            }
            if (defaults[next] && !baseUrl) setBaseUrl(defaults[next])
          }}
          className={inputCls}
          style={fieldStyle}
        >
          {providerChoices.map(p => (
            <option key={p} value={p}>
              {PROVIDER_LABELS[p] ?? p}
              {settings?.provider === p && !PROVIDERS.includes(p as typeof PROVIDERS[number]) ? ' (currently saved)' : ''}
            </option>
          ))}
        </select>
      </label>

      {/* Model */}
      <label className="flex flex-col gap-1.5">
        <span className="text-[11px]" style={{ color: T.textDim }}>Model</span>
        {customModel ? (
          <div className="flex gap-2">
            <input
              type="text"
              value={model}
              onChange={e => setModel(e.target.value)}
              placeholder={
                provider === 'ollama' ? 'qwen3-coder:30b-a3b'
                : provider === 'openai' ? 'gpt-4o'
                : 'claude-sonnet-4-20250514'
              }
              className={inputCls + ' flex-1'}
              style={inputStyle}
            />
            <SecondaryButton onClick={() => setCustomModel(false)} className="shrink-0">← Pick</SecondaryButton>
          </div>
        ) : (provider === 'ollama' || provider === 'llamacpp') ? (
          <div className="flex gap-2">
            <select
              value={model}
              onChange={e => setModel(e.target.value)}
              className={inputCls + ' flex-1'}
              style={fieldStyle}
            >
              {modelChoices.map(m => <option key={m} value={m}>{m}</option>)}
              <option value="__custom__">(custom…)</option>
            </select>
            {model === '__custom__' && (
              <PrimaryButton
                group="runtime"
                onClick={() => { setModel(''); setCustomModel(true) }}
                className="shrink-0"
              >
                Enter
              </PrimaryButton>
            )}
          </div>
        ) : (
          /* openai / anthropic — no discovery; saved model + custom escape */
          <div className="flex gap-2">
            <select
              value={model || '__custom__'}
              onChange={e => {
                if (e.target.value === '__custom__') { setCustomModel(true); setModel('') }
                else setModel(e.target.value)
              }}
              className={inputCls + ' flex-1'}
              style={fieldStyle}
            >
              {savedModelMatchesProvider && <option value={settings!.model}>{settings!.model}</option>}
              <option value="__custom__">(custom…)</option>
            </select>
          </div>
        )}
      </label>

      {/* Context budget */}
      <label className="flex flex-col gap-1.5">
        <span className="text-[11px]" style={{ color: T.textDim }}>Context budget (tokens)</span>
        <input
          type="number"
          value={contextBudget}
          onChange={e => setContextBudget(parseInt(e.target.value) || 40000)}
          className={inputCls}
          style={inputStyle}
        />
        <span className="text-[11px]" style={{ color: T.textDim }}>
          Session history is summarised when exceeding this budget.
        </span>
      </label>

      {/* Advanced disclosure */}
      <GhostButton
        type="button"
        onClick={() => setShowAdvanced(s => !s)}
        className="self-start flex items-center gap-1.5"
      >
        <span style={{
          transform: showAdvanced ? 'rotate(90deg)' : 'rotate(0deg)',
          transition: 'transform 160ms ease-out',
          display: 'inline-block',
        }}>▸</span>
        Advanced
      </GhostButton>

      {showAdvanced && (
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1.5">
            <span className="text-[11px]" style={{ color: T.textDim }}>Base URL</span>
            <input
              type="text"
              value={baseUrl}
              onChange={e => setBaseUrl(e.target.value)}
              placeholder={
                provider === 'ollama' ? 'http://localhost:11434'
                : provider === 'openai' ? 'https://api.openai.com/v1'
                : ''
              }
              className={inputCls}
              style={inputStyle}
            />
          </label>

          {showApiKey && (
            <label className="flex flex-col gap-1.5">
              <span className="text-[11px]" style={{ color: T.textDim }}>API key</span>
              <input
                type="password"
                value={apiKey}
                onChange={e => setApiKey(e.target.value)}
                placeholder={settings?.apiKeyMasked || 'Enter API key'}
                className={inputCls}
                style={inputStyle}
              />
              {settings?.apiKeyMasked && (
                <span className="text-[11px]" style={{ color: T.textDim }}>
                  Current: {settings.apiKeyMasked} — leave blank to keep
                </span>
              )}
            </label>
          )}
        </div>
      )}

      {/* Save */}
      <PrimaryButton
        group="runtime"
        onClick={() => saveMut.mutate()}
        disabled={saveMut.isPending}
        className="self-start"
      >
        {saveMut.isPending ? 'Saving…' : 'Save'}
      </PrimaryButton>
    </div>
  )
}

// ── Workspaces ───────────────────────────────────────────────────────────────

function WorkspacesSection() {
  const qc = useQueryClient()
  const [selectedName, setSelectedName] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['workspaces'],
    queryFn: () => client.listWorkspaces(create(ListWorkspacesRequestSchema, {})),
  })

  const upsertMut = useMutation({
    mutationFn: (w: { name: string; root: string; defaultBranch: string; description: string }) =>
      client.upsertWorkspace(create(UpsertWorkspaceRequestSchema, w)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['workspaces'] })
      qc.invalidateQueries({ queryKey: ['registeredTools'] })
      setCreating(false)
    },
  })

  const deleteMut = useMutation({
    mutationFn: (name: string) => client.deleteWorkspace(create(DeleteWorkspaceRequestSchema, { name })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['workspaces'] })
      setSelectedName(null)
    },
  })

  const workspaces = data?.workspaces ?? []
  const selected = workspaces.find(w => w.name === selectedName) ?? null

  return (
    <Card>
      <MicroLabel>Workspaces</MicroLabel>
      <div className="grid grid-cols-[220px_1fr] gap-4">
        <aside className="flex flex-col gap-1">
          <PrimaryButton
            group="runtime"
            onClick={() => { setCreating(true); setSelectedName(null) }}
          >
            + New workspace
          </PrimaryButton>
          {isLoading && <span className="text-[12px]" style={{ color: T.textDim }}>Loading…</span>}
          {workspaces.map(w => (
            <button
              key={w.name}
              onClick={() => { setSelectedName(w.name); setCreating(false) }}
              className="text-left text-[13px] px-2 py-1.5 rounded"
              style={{
                background: w.name === selectedName ? T.surface : 'transparent',
                color: T.textBright,
                borderLeft: w.name === 'default' ? `2px solid ${T.accent.runtime}` : '2px solid transparent',
              }}
            >
              <div>{w.name}</div>
              <div className="text-[11px]" style={{ color: T.textDim }}>{w.root}</div>
            </button>
          ))}
        </aside>
        <WorkspaceEditor
          key={creating ? '__new__' : (selectedName ?? '')}
          workspace={creating ? null : selected}
          onSave={(w) => upsertMut.mutate(w)}
          onDelete={(name) => deleteMut.mutate(name)}
          saveError={(upsertMut.error as Error | null)?.message ?? null}
        />
      </div>
    </Card>
  )
}

function WorkspaceEditor({
  workspace,
  onSave,
  onDelete,
  saveError,
}: {
  workspace: WorkspaceInfo | null
  onSave: (w: { name: string; root: string; defaultBranch: string; description: string }) => void
  onDelete: (name: string) => void
  saveError: string | null
}) {
  const [name, setName] = useState(workspace?.name ?? '')
  const [root, setRoot] = useState(workspace?.root ?? '')
  const [defaultBranch, setDefaultBranch] = useState(workspace?.defaultBranch ?? '')
  const [description, setDescription] = useState(workspace?.description ?? '')

  const isExisting = !!workspace
  const isDefault = workspace?.name === 'default'
  // tokens.ts has no T.status.warn — using T.accent.review (amber) as the
  // non-fatal warning sibling; T.status.error (red) would overstate severity.
  const warnColor = T.accent.review

  return (
    <div className="flex flex-col gap-3">
      <label className="flex flex-col gap-1">
        <span className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>Name</span>
        <input
          value={name}
          onChange={e => setName(e.target.value)}
          disabled={isExisting}
          className={inputCls}
          style={inputStyle}
          placeholder="e.g. zarl"
        />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>Root (absolute path)</span>
        <input
          value={root}
          onChange={e => setRoot(e.target.value)}
          className={inputCls}
          style={inputStyle}
          placeholder="/home/user/src/project"
        />
        {workspace && !workspace.rootExists && (
          <span className="text-[11px]" style={{ color: warnColor }}>
            path does not exist on disk
          </span>
        )}
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>Default branch</span>
        <input
          value={defaultBranch}
          onChange={e => setDefaultBranch(e.target.value)}
          className={inputCls}
          style={inputStyle}
          placeholder="main"
        />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>Description</span>
        <textarea
          value={description}
          onChange={e => setDescription(e.target.value)}
          className={inputCls}
          style={inputStyle}
          rows={2}
        />
      </label>
      {saveError && (
        <span className="text-[11px]" style={{ color: T.status.error }}>{saveError}</span>
      )}
      <div className="flex gap-2 mt-1">
        <PrimaryButton
          group="runtime"
          onClick={() => onSave({ name, root, defaultBranch, description })}
          disabled={!name || !root}
        >Save</PrimaryButton>
        {isExisting && !isDefault && (
          <DangerButton onClick={() => onDelete(workspace.name)}>Delete</DangerButton>
        )}
      </div>
    </div>
  )
}

// ── Root ─────────────────────────────────────────────────────────────────────

export default function TaskRunnerView() {
  return (
    <div className="flex flex-col gap-5 min-w-0 max-w-[600px]">
      <Card>
        <MicroLabel>Task runner LLM</MicroLabel>
        <TaskRunnerForm />
      </Card>
      <WorkspacesSection />
    </div>
  )
}

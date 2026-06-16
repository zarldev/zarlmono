import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  ListToolProvidersRequestSchema,
  ListRegisteredToolsRequestSchema,
  UpdateToolProviderRequestSchema,
  CreateToolProviderRequestSchema,
  DeleteToolProviderRequestSchema,
  UpdateToolDescriptionRequestSchema,
  ResetToolDescriptionRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import Drawer from '../Drawer'
import { T } from '../tokens'
import { Card, MicroLabel, PrimaryButton, SecondaryButton, DangerButton, GhostButton } from '../primitives'

const inputCls = 'px-3 py-1.5 rounded-lg border bg-transparent text-[13px] outline-none w-full'
const inputStyle = { color: T.textBright, borderColor: T.border }

// Inline Toggle — copied from IdentityView's private const but uses runtime accent.
function Toggle({ on, onChange }: { on: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      onClick={() => onChange(!on)}
      className="relative inline-flex shrink-0 transition-colors"
      style={{
        width: 36,
        height: 20,
        borderRadius: 999,
        background: on ? T.accent.runtime : T.border,
      }}
    >
      <span
        className="absolute top-0.5 transition-transform"
        style={{
          width: 16,
          height: 16,
          borderRadius: '50%',
          background: T.textBright,
          transform: on ? 'translateX(18px)' : 'translateX(2px)',
        }}
      />
    </button>
  )
}

// ── Section A — Providers ────────────────────────────────────────────────────

type ProviderDrawerPayload = {
  id: string
  name: string
  enabled: boolean
  configObj: Record<string, string>
} | null

function ProvidersSection() {
  const qc = useQueryClient()
  const [drawerPayload, setDrawerPayload] = useState<ProviderDrawerPayload>(null)
  const [drawerDraft, setDrawerDraft] = useState<Record<string, string>>({})
  const [showAddMcp, setShowAddMcp] = useState(false)
  const [mcpName, setMcpName] = useState('')
  const [mcpUrl, setMcpUrl] = useState('')
  const [mcpToken, setMcpToken] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['toolProviders'],
    queryFn: () => client.listToolProviders(create(ListToolProvidersRequestSchema, {})),
  })

  const toggleMut = useMutation({
    mutationFn: (args: { id: string; enabled: boolean; config: string }) =>
      client.updateToolProvider(create(UpdateToolProviderRequestSchema, args)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['toolProviders'] }),
  })

  const saveConfigMut = useMutation({
    mutationFn: (args: { id: string; enabled: boolean; config: string }) =>
      client.updateToolProvider(create(UpdateToolProviderRequestSchema, args)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['toolProviders'] })
      setDrawerPayload(null)
    },
  })

  const createMcpMut = useMutation({
    mutationFn: (args: { name: string; type: string; config: string }) =>
      client.createToolProvider(create(CreateToolProviderRequestSchema, args)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['toolProviders'] })
      setShowAddMcp(false)
      setMcpName('')
      setMcpUrl('')
      setMcpToken('')
    },
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) =>
      client.deleteToolProvider(create(DeleteToolProviderRequestSchema, { id })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['toolProviders'] }),
  })

  const providers = data?.providers ?? []

  function openConfigure(p: typeof providers[number]) {
    let configObj: Record<string, string> = {}
    try { configObj = JSON.parse(p.config || '{}') as Record<string, string> } catch { /* empty */ }
    setDrawerDraft(configObj)
    setDrawerPayload({ id: p.id, name: p.name, enabled: p.enabled, configObj })
  }

  return (
    <>
      <Card>
        <MicroLabel>Providers</MicroLabel>

        {isLoading && (
          <p className="text-[12px]" style={{ color: T.textDim }}>Loading…</p>
        )}

        <div className="flex flex-col gap-2">
          {providers.map(p => (
            <div
              key={p.id}
              className="flex items-center gap-3 px-3 py-2.5 rounded-xl border"
              style={{ borderColor: T.border, background: T.surface }}
            >
              {/* Toggle */}
              <Toggle
                on={p.enabled}
                onChange={enabled => toggleMut.mutate({ id: p.id, enabled, config: p.config })}
              />

              {/* Name */}
              <span className="text-[13px] font-semibold flex-1" style={{ color: T.textBright }}>
                {p.name}
              </span>

              {/* Type pill */}
              <span
                className="text-[10px] px-1.5 py-0.5 rounded uppercase tracking-wider shrink-0"
                style={
                  p.type === 'mcp'
                    ? { background: `${T.accent.runtime}22`, color: T.accent.runtime }
                    : { background: 'rgba(255,255,255,0.06)', color: T.textDim }
                }
              >
                {p.type}
              </span>

              {/* Tool count */}
              {Number(p.toolCount) > 0 && (
                <span
                  className="text-[10px] px-1.5 py-0.5 rounded shrink-0"
                  style={{ background: 'rgba(255,255,255,0.06)', color: T.textDim }}
                >
                  {String(p.toolCount)} tools
                </span>
              )}

              {/* Configure */}
              <SecondaryButton onClick={() => openConfigure(p)} className="shrink-0">Configure</SecondaryButton>

              {/* Delete (mcp only) */}
              {p.type === 'mcp' && (
                <DangerButton
                  onClick={() => { if (confirm('Delete this provider?')) deleteMut.mutate(p.id) }}
                  className="shrink-0"
                >
                  Delete
                </DangerButton>
              )}
            </div>
          ))}
        </div>

        {/* Add MCP form / button */}
        {showAddMcp ? (
          <div
            className="flex flex-col gap-2 p-4 rounded-xl border"
            style={{ borderColor: T.border, background: T.surface }}
          >
            <MicroLabel>New MCP server</MicroLabel>
            <input
              value={mcpName}
              onChange={e => setMcpName(e.target.value)}
              placeholder="Server name"
              className={inputCls}
              style={inputStyle}
            />
            <input
              value={mcpUrl}
              onChange={e => setMcpUrl(e.target.value)}
              placeholder="URL"
              className={inputCls}
              style={inputStyle}
            />
            <input
              value={mcpToken}
              onChange={e => setMcpToken(e.target.value)}
              placeholder="Auth token (optional)"
              className={inputCls}
              style={inputStyle}
            />
            <div className="flex gap-2 justify-end pt-1">
              <SecondaryButton onClick={() => { setShowAddMcp(false); setMcpName(''); setMcpUrl(''); setMcpToken('') }}>
                Cancel
              </SecondaryButton>
              <PrimaryButton
                group="runtime"
                onClick={() => {
                  const cfg: Record<string, string> = { url: mcpUrl }
                  if (mcpToken) cfg.auth_token = mcpToken
                  createMcpMut.mutate({ name: mcpName, type: 'mcp', config: JSON.stringify(cfg) })
                }}
                disabled={createMcpMut.isPending || !mcpName.trim() || !mcpUrl.trim()}
              >
                {createMcpMut.isPending ? 'Creating…' : 'Create'}
              </PrimaryButton>
            </div>
          </div>
        ) : (
          <GhostButton onClick={() => setShowAddMcp(true)} className="self-start">
            + Add MCP server
          </GhostButton>
        )}
      </Card>

      {/* Configure drawer */}
      <Drawer
        open={drawerPayload !== null}
        onClose={() => setDrawerPayload(null)}
        title={drawerPayload && (
          <span className="text-[18px] font-semibold">{drawerPayload.name}</span>
        )}
      >
        {drawerPayload && (
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-3">
              {Object.keys(drawerDraft).length === 0 && (
                <p className="text-[12px]" style={{ color: T.textDim }}>No configuration fields.</p>
              )}
              {Object.entries(drawerDraft).map(([key, val]) => (
                <label key={key} className="flex flex-col gap-1.5">
                  <span className="text-[11px]" style={{ color: T.textDim }}>{key}</span>
                  <input
                    value={val}
                    onChange={e => setDrawerDraft(prev => ({ ...prev, [key]: e.target.value }))}
                    className={inputCls}
                    style={inputStyle}
                  />
                </label>
              ))}
            </div>
            <div className="flex gap-2 pt-2">
              <SecondaryButton onClick={() => setDrawerPayload(null)}>Cancel</SecondaryButton>
              <PrimaryButton
                group="runtime"
                onClick={() => saveConfigMut.mutate({
                  id: drawerPayload.id,
                  enabled: drawerPayload.enabled,
                  config: JSON.stringify(drawerDraft),
                })}
                disabled={saveConfigMut.isPending}
              >
                {saveConfigMut.isPending ? 'Saving…' : 'Save'}
              </PrimaryButton>
            </div>
          </div>
        )}
      </Drawer>
    </>
  )
}

// ── Section B — Registered tools ─────────────────────────────────────────────

type RegisteredTool = {
  name: string
  description: string
  defaultDescription: string
  hasOverride: boolean
  provider: string
  category: string
  parameters: { name: string; type: string; description: string; required: boolean; enumValues: string[] }[]
}

function RegisteredToolsSection() {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [draft, setDraft] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['registeredTools'],
    queryFn: () => client.listRegisteredTools(create(ListRegisteredToolsRequestSchema, {})),
  })

  const updateMut = useMutation({
    mutationFn: (args: { name: string; description: string }) =>
      client.updateToolDescription(create(UpdateToolDescriptionRequestSchema, args)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['registeredTools'] })
      setEditing(null)
    },
  })

  const resetMut = useMutation({
    mutationFn: (name: string) =>
      client.resetToolDescription(create(ResetToolDescriptionRequestSchema, { name })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['registeredTools'] })
      setEditing(null)
    },
  })

  const tools: RegisteredTool[] = (data?.tools ?? []) as RegisteredTool[]

  if (isLoading) return (
    <Card>
      <MicroLabel>Registered tools</MicroLabel>
      <p className="text-[12px]" style={{ color: T.textDim }}>Loading…</p>
    </Card>
  )

  if (tools.length === 0) return (
    <Card>
      <MicroLabel>Registered tools</MicroLabel>
      <p className="text-[12px]" style={{ color: T.textDim }}>No tools registered.</p>
    </Card>
  )

  // Group by provider (or action-tool bucket), native first.
  const groups = new Map<string, RegisteredTool[]>()
  for (const t of tools) {
    const key = t.category === 'action' ? 'action' : (t.provider || 'native')
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key)!.push(t)
  }
  const ordered = Array.from(groups.entries()).sort(([a], [b]) => {
    if (a === 'native') return -1
    if (b === 'native') return 1
    if (a === 'action') return 1
    if (b === 'action') return -1
    return a.localeCompare(b)
  })

  return (
    <Card>
      <MicroLabel>Registered tools</MicroLabel>
      <div className="flex flex-col gap-4">
        {ordered.map(([group, items]) => (
          <div key={group} className="flex flex-col gap-1.5">
            {/* Group label */}
            <div className="flex items-center gap-2">
              <span className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>
                {group === 'native' ? 'Built-in' : group === 'action' ? 'Action (taskrunner)' : group}
              </span>
              <span className="text-[10px]" style={{ color: T.textDim }}>({items.length})</span>
            </div>

            {/* Tool rows */}
            <div className="flex flex-col gap-1">
              {items.map(t => {
                const isOpen = expanded === t.name
                const isEditing = editing === t.name
                return (
                  <div
                    key={t.name}
                    className="rounded-lg border overflow-hidden"
                    style={{ borderColor: T.border, background: T.surface }}
                  >
                    <button
                      onClick={() => setExpanded(isOpen ? null : t.name)}
                      className="w-full flex items-center gap-3 px-3 py-2 text-left hover:bg-white/[0.02] transition-colors"
                    >
                      <span className="text-[12px] font-semibold font-mono shrink-0" style={{ color: T.textBright }}>
                        {t.name}
                      </span>
                      {t.hasOverride && (
                        <span
                          className="text-[9px] px-1.5 py-0.5 rounded uppercase tracking-wider shrink-0"
                          style={{ background: `${T.accent.runtime}22`, color: T.accent.runtime }}
                        >
                          edited
                        </span>
                      )}
                      <span className="text-[11px] flex-1 truncate" style={{ color: T.textDim }}>
                        {t.description.split('.')[0]}
                      </span>
                      <span className="text-[10px] shrink-0" style={{ color: T.textDim }}>
                        {isOpen ? '−' : '+'}
                      </span>
                    </button>

                    {isOpen && (
                      <div
                        className="px-3 pb-3 pt-2 flex flex-col gap-2 border-t"
                        style={{ borderColor: T.border }}
                      >
                        {!isEditing ? (
                          <>
                            <p className="text-[12px] leading-relaxed whitespace-pre-wrap" style={{ color: T.text }}>
                              {t.description}
                            </p>
                            {t.hasOverride && (
                              <details className="text-[11px]" style={{ color: T.textDim }}>
                                <summary className="cursor-pointer">Code default</summary>
                                <p className="mt-1 leading-relaxed whitespace-pre-wrap" style={{ color: T.textDim }}>
                                  {t.defaultDescription}
                                </p>
                              </details>
                            )}
                            <div className="flex gap-2 pt-1">
                              <SecondaryButton onClick={() => { setEditing(t.name); setDraft(t.description) }}>
                                Edit description
                              </SecondaryButton>
                              {t.hasOverride && (
                                <DangerButton
                                  onClick={() => { if (confirm('Reset to code default?')) resetMut.mutate(t.name) }}
                                  disabled={resetMut.isPending}
                                >
                                  Reset to default
                                </DangerButton>
                              )}
                            </div>
                          </>
                        ) : (
                          <>
                            <textarea
                              value={draft}
                              onChange={e => setDraft(e.target.value)}
                              rows={6}
                              className="px-3 py-2 rounded-lg border bg-transparent text-[12px] outline-none leading-relaxed font-mono"
                              style={{ color: T.textBright, borderColor: T.border, resize: 'vertical' }}
                            />
                            <div className="flex gap-2 justify-end">
                              <SecondaryButton onClick={() => setEditing(null)}>Cancel</SecondaryButton>
                              <PrimaryButton
                                group="runtime"
                                onClick={() => updateMut.mutate({ name: t.name, description: draft })}
                                disabled={updateMut.isPending || draft.trim() === ''}
                              >
                                {updateMut.isPending ? 'Saving…' : 'Save'}
                              </PrimaryButton>
                            </div>
                          </>
                        )}
                        {t.parameters.length > 0 && (
                          <div className="flex flex-col gap-1 mt-1">
                            <span className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>
                              Parameters
                            </span>
                            {t.parameters.map(p => (
                              <div key={p.name} className="flex items-start gap-2 text-[11px]">
                                <span className="font-mono shrink-0" style={{ color: T.textBright }}>{p.name}</span>
                                <span className="shrink-0" style={{ color: T.textDim }}>
                                  {p.type}{p.required ? '*' : ''}
                                  {p.enumValues.length > 0 && ` [${p.enumValues.join('|')}]`}
                                </span>
                                <span className="flex-1" style={{ color: T.text }}>{p.description}</span>
                              </div>
                            ))}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        ))}
      </div>
    </Card>
  )
}

// ── Root ─────────────────────────────────────────────────────────────────────

export default function ToolsView() {
  return (
    <div className="flex flex-col gap-5 min-w-0">
      <ProvidersSection />
      <RegisteredToolsSection />
    </div>
  )
}

import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import {
  GetAgentNameRequestSchema,
  SetAgentNameRequestSchema,
  GetVoiceSettingsRequestSchema,
  SetVoiceSettingsRequestSchema,
  PreviewVoiceRequestSchema,
  GetConversationLLMSettingsRequestSchema,
  UpdateConversationLLMSettingsRequestSchema,
  ListAvailableModelsRequestSchema,
  GetAgentAvatarRequestSchema,
  SetAgentAvatarRequestSchema,
  ResetAgentStateRequestSchema,
} from '@/gen/zarl/v1/admin_pb'
import { client } from '@/admin/shared'
import { T } from '../tokens'
import { Card, MicroLabel, PrimaryButton, SecondaryButton, DangerButton, GhostButton } from '../primitives'
import { AVATARS, DEFAULT_AVATAR, avatarById } from '@/avatars'
import type { Avatar } from '@/avatars'
import TalkingHeadLib from '@/TalkingHeadLib'
import type { GestureCue, GestureName, MoodName } from '@/hooks/usePresenceSession'

// ── Audio preview helper ─────────────────────────────────────────────────────

async function playAudioPreview(
  speaker: number,
  speed: number,
  text: string,
  onStart: () => void,
  onEnd: () => void,
) {
  onStart()
  try {
    const resp = await client.previewVoice(create(PreviewVoiceRequestSchema, { speaker, speed, text }))
    const pcm = resp.pcm
    if (!pcm.length) { onEnd(); return }
    const ctx = new AudioContext({ sampleRate: resp.sampleRate })
    const int16 = new Int16Array(pcm.buffer, pcm.byteOffset, pcm.byteLength / 2)
    const float32 = new Float32Array(int16.length)
    for (let i = 0; i < int16.length; i++) float32[i] = int16[i] / 32768
    const buf = ctx.createBuffer(1, float32.length, ctx.sampleRate)
    buf.getChannelData(0).set(float32)
    const source = ctx.createBufferSource()
    source.buffer = buf
    source.connect(ctx.destination)
    source.start()
    source.onended = () => { onEnd(); ctx.close() }
  } catch {
    onEnd()
  }
}

const VOICE_CHIP_LIMIT = 11

function engineLabel(name: string): string {
  if (!name) return ''
  return name.charAt(0).toUpperCase() + name.slice(1)
}

const inputCls = 'px-3 py-1.5 rounded-lg border bg-transparent text-[13px] outline-none w-full'
const inputStyle = { color: T.textBright, borderColor: T.border }

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
        background: on ? T.accent.identity : T.border,
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

// ── Row A — Names ────────────────────────────────────────────────────────────

function RowA() {
  const qc = useQueryClient()

  const { data: nameData } = useQuery({
    queryKey: ['agentName'],
    queryFn: () => client.getAgentName(create(GetAgentNameRequestSchema, {})),
  })
  const { data: voiceData } = useQuery({
    queryKey: ['voiceSettings'],
    queryFn: () => client.getVoiceSettings(create(GetVoiceSettingsRequestSchema, {})),
  })

  const [display, setDisplay] = useState('')
  const [spoken, setSpoken] = useState('')
  const [initialized, setInitialized] = useState(false)
  const [namePreviewing, setNamePreviewing] = useState(false)

  if (nameData && !initialized) {
    setDisplay(nameData.displayName || '')
    setSpoken(nameData.spokenName || nameData.displayName || '')
    setInitialized(true)
  }

  const saveMut = useMutation({
    mutationFn: () => {
      const d = display.trim()
      const s = spoken.trim()
      return client.setAgentName(create(SetAgentNameRequestSchema, {
        displayName: d,
        // When spoken === display, send empty so backend stores '' (substitution off)
        spokenName: s === d ? '' : s,
      }))
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['agentName'] })
      setInitialized(false)
    },
  })

  // Server stores spokenName='' when it equals displayName (substitution off).
  // The form auto-fills the spoken field with displayName for editability, so
  // we compare against that same convention to decide whether the form is dirty.
  const d = display.trim()
  const s = spoken.trim()
  const effectiveSpoken = s === d ? '' : s
  const dirty =
    d !== '' && (
      d !== (nameData?.displayName ?? '') ||
      effectiveSpoken !== (nameData?.spokenName ?? '')
    )

  return (
    <div className="flex items-end gap-4">
      {/* Display name */}
      <label className="flex flex-col gap-1.5 flex-1">
        <span className="text-[11px]" style={{ color: T.textDim }}>Display name</span>
        <input
          type="text"
          value={display}
          onChange={e => setDisplay(e.target.value)}
          placeholder="Zarl"
          className={inputCls}
          style={inputStyle}
        />
      </label>

      {/* Spoken name + preview */}
      <label className="flex flex-col gap-1.5 flex-1">
        <span className="text-[11px]" style={{ color: T.textDim }}>Spoken name</span>
        <div className="flex gap-2">
          <input
            type="text"
            value={spoken}
            onChange={e => setSpoken(e.target.value)}
            placeholder="Zarl"
            className={inputCls + ' flex-1'}
            style={inputStyle}
          />
          <SecondaryButton
            onClick={() => playAudioPreview(
              voiceData?.speaker ?? 0,
              voiceData?.speed ?? 1.1,
              spoken.trim() || display.trim() || 'Zarl',
              () => setNamePreviewing(true),
              () => setNamePreviewing(false),
            )}
            disabled={namePreviewing}
            className="shrink-0"
          >
            {namePreviewing ? 'Playing…' : '▷ Preview'}
          </SecondaryButton>
        </div>
      </label>

      {/* Save */}
      <PrimaryButton
        group="identity"
        onClick={() => saveMut.mutate()}
        disabled={!dirty || saveMut.isPending}
        className="shrink-0 self-end mb-[1px]"
      >
        {saveMut.isPending ? 'Saving…' : 'Save'}
      </PrimaryButton>
    </div>
  )
}

// ── Row B — 3D viewer (full-width) ──────────────────────────────────────────

function ViewerRow({ currentAvatar, gestureCue }: { currentAvatar: Avatar; gestureCue: GestureCue | null }) {
  // No explicit height — the grid row stretches both children to the same
  // size, so the viewer matches the right-column stack of options exactly.
  // min-height is a floor in case the column ever shrinks unexpectedly.
  return (
    <div
      className="relative rounded-xl border overflow-hidden h-full"
      style={{
        minHeight: 360,
        background: T.surface,
        borderColor: T.border,
      }}
    >
      <div
        className="cursor-grab active:cursor-grabbing"
        style={{ position: 'absolute', inset: 0, width: '100%', height: '100%' }}
      >
        <TalkingHeadLib
          analyser={null}
          state="listening"
          avatar={currentAvatar}
          gestureCue={gestureCue}
          interactive
        />
      </div>
    </div>
  )
}

// ── Gesture playground ──────────────────────────────────────────────────────

const GESTURE_NAMES: GestureName[] = [
  'handup', 'index', 'ok', 'thumbup', 'thumbdown', 'side', 'shrug', 'namaste',
  'wave', 'peace', 'stop', 'pointself', 'fistpump', 'beckon',
]
const MOOD_NAMES: MoodName[] = [
  'neutral', 'happy', 'angry', 'sad', 'fear', 'disgust', 'love', 'sleep',
]

function GesturePlaygroundSection({ onFire }: { onFire: (cue: Omit<GestureCue, 'ts'>) => void }) {
  const [pinnedMood, setPinnedMood] = useState<MoodName | null>(null)
  return (
    <Card>
      <MicroLabel>Gesture playground</MicroLabel>
      <p className="text-[11px]" style={{ color: T.textDim }}>
        Click a gesture to fire it on the preview viewer. Pin a mood to hold it while testing gestures; tap "clear" to revert.
      </p>

      <div className="flex flex-col gap-1.5">
        <div className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>Gestures</div>
        <div className="flex flex-wrap gap-1.5">
          {GESTURE_NAMES.map(g => (
            <button
              key={g}
              onClick={() => onFire({ gesture: g, mood: pinnedMood ?? undefined, moodHoldMs: 2500 })}
              className="text-[11px] px-2.5 py-1 rounded-lg border font-mono"
              style={{ color: T.textBright, borderColor: T.border, background: T.surface }}
            >
              {g}
            </button>
          ))}
        </div>
      </div>

      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <div className="text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>Moods</div>
          {pinnedMood && (
            <button
              onClick={() => { setPinnedMood(null); onFire({ mood: 'neutral', moodHoldMs: 400 }) }}
              className="text-[10px] px-2 py-0.5 rounded border"
              style={{ color: T.status.error, borderColor: `${T.status.error}40` }}
            >
              clear ({pinnedMood})
            </button>
          )}
        </div>
        <div className="flex flex-wrap gap-1.5">
          {MOOD_NAMES.map(m => {
            const pinned = pinnedMood === m
            return (
              <button
                key={m}
                onClick={() => {
                  const next = pinned ? null : m
                  setPinnedMood(next)
                  onFire({ mood: next ?? 'neutral', moodHoldMs: next ? 60_000 : 400 })
                }}
                className="text-[11px] px-2.5 py-1 rounded-lg border font-mono"
                style={
                  pinned
                    ? { color: T.accent.runtime, borderColor: `${T.accent.runtime}80`, background: `${T.accent.runtime}1a` }
                    : { color: T.textBright, borderColor: T.border, background: T.surface }
                }
              >
                {m}
              </button>
            )
          })}
        </div>
      </div>
    </Card>
  )
}

// ── Row C — Avatar swatches (horizontal strip above voice) ──────────────────

function AvatarRow({ currentAvatar, onAvatarChange }: {
  currentAvatar: Avatar
  onAvatarChange: (av: Avatar) => void
}) {
  const qc = useQueryClient()
  const [avatarError, setAvatarError] = useState<string | null>(null)

  const setMut = useMutation({
    mutationFn: (avatarId: string) =>
      client.setAgentAvatar(create(SetAgentAvatarRequestSchema, { avatarId })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['agentAvatar'] })
      setAvatarError(null)
    },
    onError: (_err, avatarId) => {
      const saved = qc.getQueryData<{ avatarId: string }>(['agentAvatar'])
      const revertTo = avatarById(saved?.avatarId ?? DEFAULT_AVATAR.id)
      onAvatarChange(revertTo)
      setAvatarError(`Couldn't save avatar "${avatarId}"`)
    },
  })

  function selectAvatar(av: Avatar) {
    setAvatarError(null)
    onAvatarChange(av)
    setMut.mutate(av.id)
  }

  return (
    <div className="flex flex-col gap-2">
      <MicroLabel>Avatar</MicroLabel>
      <div className="grid grid-cols-2 gap-2">
        {AVATARS.map(av => {
          const isActive = av.id === currentAvatar.id
          return (
            <button
              key={av.id}
              onClick={() => selectAvatar(av)}
              className="flex items-center justify-between gap-2 px-3 py-2 rounded-lg border transition-colors"
              style={{
                background: isActive ? `${T.accent.identity}12` : T.surface,
                borderColor: isActive ? T.accent.identity : T.border,
              }}
            >
              <span
                className="text-[12px] font-semibold truncate"
                style={{ color: isActive ? T.accent.identity : T.textBright }}
              >
                {av.label}
              </span>
              <span
                className="text-[9px] uppercase tracking-wider px-1 py-0.5 rounded shrink-0"
                style={{ color: T.textDim, background: 'rgba(255,255,255,0.05)' }}
              >
                {av.body}
              </span>
            </button>
          )
        })}
      </div>
      {avatarError && (
        <p className="text-[11px]" style={{ color: T.status.error }}>{avatarError}</p>
      )}
    </div>
  )
}

// ── Row D — Voice (single chip row + speed slider) ──────────────────────────

function VoiceBar() {
  const qc = useQueryClient()
  const [speed, setSpeed] = useState(1.1)
  const [initialized, setInitialized] = useState(false)
  const [previewing, setPreviewing] = useState<number | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['voiceSettings'],
    queryFn: () => client.getVoiceSettings(create(GetVoiceSettingsRequestSchema, {})),
  })

  if (data && !initialized) {
    setSpeed(data.speed)
    setInitialized(true)
  }

  const activeSpeaker = data?.speaker ?? 0
  const activeEngine = data?.engine ?? ''
  const availableEngines = data?.availableEngines ?? []
  const numSpeakers = data?.numSpeakers ?? 0
  const chipCount = Math.min(Math.max(numSpeakers, 0), VOICE_CHIP_LIMIT)
  const voices = Array.from({ length: chipCount }, (_, i) => ({ id: i }))

  // One mutation: pick a chip → preview AND save in one click. The TTS
  // synth picks up the new speaker on the next utterance.
  const selectMut = useMutation({
    mutationFn: (speaker: number) =>
      client.setVoiceSettings(create(SetVoiceSettingsRequestSchema, { speaker, speed })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['voiceSettings'] }),
  })

  const speedMut = useMutation({
    mutationFn: (s: number) =>
      client.setVoiceSettings(create(SetVoiceSettingsRequestSchema, { speaker: activeSpeaker, speed: s })),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['voiceSettings'] }); setInitialized(false) },
  })

  // Engine switch — sends speaker=0 so the new engine starts at its
  // first voice; the backend then writes voice.<engine> = "0:speed".
  // The next time you switch back, the per-engine value restores
  // whichever speaker was last active on that engine.
  const engineMut = useMutation({
    mutationFn: (engine: string) =>
      client.setVoiceSettings(create(SetVoiceSettingsRequestSchema, { engine, speaker: 0, speed })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['voiceSettings'] }),
  })

  function pickVoice(id: number) {
    selectMut.mutate(id)
    playAudioPreview(id, speed, '', () => setPreviewing(id), () => setPreviewing(null))
  }

  if (isLoading) return null

  return (
    <div className="flex flex-col gap-3">
      <MicroLabel>Voice</MicroLabel>

      {availableEngines.length > 1 && (
        <div className="flex items-center gap-1.5 flex-wrap">
          <span className="text-[11px]" style={{ color: T.textDim }}>Engine</span>
          {availableEngines.map(name => {
            const isActive = name === activeEngine
            return (
              <button
                key={name}
                onClick={() => engineMut.mutate(name)}
                disabled={isActive || engineMut.isPending}
                className="text-[12px] font-medium h-[28px] px-3 rounded-lg border transition-colors disabled:cursor-default"
                title={isActive ? `${engineLabel(name)} (active)` : `Switch to ${engineLabel(name)}`}
                style={{
                  background: isActive ? T.accent.identity : T.surface,
                  borderColor: isActive ? T.accent.identity : T.border,
                  color: isActive ? T.bg : T.textBright,
                }}
              >
                {engineLabel(name)}
              </button>
            )
          })}
        </div>
      )}

      {/* Speed slider */}
      <div className="flex items-center gap-3">
        <span className="text-[11px]" style={{ color: T.textDim }}>Speed</span>
        <input
          type="range"
          min={0.5}
          max={2.0}
          step={0.1}
          value={speed}
          onChange={e => setSpeed(Number(e.target.value))}
          onMouseUp={() => speedMut.mutate(speed)}
          onTouchEnd={() => speedMut.mutate(speed)}
          className="flex-1 max-w-[280px]"
          style={{ accentColor: T.accent.identity }}
        />
        <span
          className="text-[14px] font-semibold tabular-nums w-[4ch]"
          style={{ color: T.textBright }}
        >
          {speed.toFixed(1)}×
        </span>
      </div>

      {/* Voice chips: click = preview + select in one step */}
      <div className="grid grid-cols-6 gap-1.5">
        {voices.map(v => {
          const isActive = v.id === activeSpeaker
          const isPlaying = previewing === v.id
          const num = String(v.id + 1).padStart(2, '0')
          return (
            <button
              key={v.id}
              onClick={() => pickVoice(v.id)}
              disabled={isPlaying || selectMut.isPending}
              className="text-[12px] font-mono tabular-nums h-[32px] rounded-lg border transition-colors disabled:cursor-default"
              title={isActive ? `Voice ${num} (active)` : `Voice ${num} — click to preview & select`}
              style={{
                background: isActive ? T.accent.identity : isPlaying ? `${T.accent.identity}28` : T.surface,
                borderColor: isActive || isPlaying ? T.accent.identity : T.border,
                color: isActive ? T.bg : isPlaying ? T.accent.identity : T.textBright,
              }}
            >
              {num}
            </button>
          )
        })}
      </div>
    </div>
  )
}

// ── Conversation model form ─────────────────────────────────────────────────

function ConversationModelForm() {
  const qc = useQueryClient()

  const { data: settings, isLoading } = useQuery({
    queryKey: ['conversationLLMSettings'],
    queryFn: () => client.getConversationLLMSettings(create(GetConversationLLMSettingsRequestSchema, {})),
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
  const [reasoning, setReasoning] = useState(false)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const [initialized, setInitialized] = useState(false)

  if (settings && !initialized) {
    setProvider(settings.provider || 'ollama')
    setModel(settings.model || '')
    setBaseUrl(settings.baseUrl || '')
    setReasoning(Boolean(settings.reasoningEnabled))
    setInitialized(true)
  }

  // Provider/model choices come from what discovery actually returned.
  // The currently-saved provider/model is always included so we never hide
  // a valid configuration just because the server is briefly unreachable.
  const discoveredAll = available?.models ?? []
  const providerChoices = Array.from(new Set([
    ...(settings?.provider ? [settings.provider] : []),
    ...discoveredAll.map(m => m.provider),
  ]))
  const modelChoices = Array.from(new Set([
    ...(settings?.model ? [settings.model] : []),
    ...discoveredAll.filter(m => m.provider === provider).map(m => m.name),
  ]))
  const PROVIDER_LABELS: Record<string, string> = {
    ollama:   'Ollama (local)',
    llamacpp: 'llama.cpp server',
  }

  const updateMut = useMutation({
    mutationFn: () => client.updateConversationLLMSettings(create(UpdateConversationLLMSettingsRequestSchema, {
      provider,
      model,
      baseUrl,
      apiKey,
      reasoningEnabled: reasoning,
    })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['conversationLLMSettings'] })
      setApiKey('')
    },
  })

  if (isLoading) return null

  const fieldStyle = { ...inputStyle, background: T.surface }

  return (
    <div className="flex flex-col gap-3">
        <label className="flex flex-col gap-1.5">
          <span className="text-[11px]" style={{ color: T.textDim }}>Provider</span>
          <select
            value={provider}
            onChange={e => { setProvider(e.target.value); setModel('') }}
            className={inputCls}
            style={fieldStyle}
          >
            {providerChoices.length === 0 && <option value="">(no providers reachable)</option>}
            {providerChoices.map(p => {
              const count = discoveredAll.filter(m => m.provider === p).length
              const label = PROVIDER_LABELS[p] ?? p
              return (
                <option key={p} value={p}>
                  {label}{count > 0 ? ` · ${count} model${count === 1 ? '' : 's'}` : ''}
                </option>
              )
            })}
          </select>
        </label>

        <label className="flex flex-col gap-1.5">
          <span className="text-[11px]" style={{ color: T.textDim }}>Model</span>
          {customModel ? (
            <div className="flex gap-2">
              <input
                type="text"
                value={model}
                onChange={e => setModel(e.target.value)}
                placeholder={provider === 'ollama' ? 'gemma4:26b' : 'qwen3.6-35b-a3b'}
                className={inputCls + ' flex-1'}
                style={inputStyle}
              />
              <SecondaryButton onClick={() => setCustomModel(false)} className="shrink-0">← Pick</SecondaryButton>
            </div>
          ) : (
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
                  group="identity"
                  onClick={() => { setModel(''); setCustomModel(true) }}
                  className="shrink-0"
                >
                  Enter
                </PrimaryButton>
              )}
            </div>
          )}
        </label>

        {/* Thinking — compact toggle row */}
        <label className="flex items-center justify-between gap-3 cursor-pointer select-none">
          <span className="text-[12px]" style={{ color: T.text }}>
            Thinking
            {model.toLowerCase().includes('gemma') && (
              <span className="ml-1.5 text-[10px]" style={{ color: T.textDim }}>
                (prepends <code style={{ color: T.accent.review }}>{'<|think|>'}</code>)
              </span>
            )}
          </span>
          <Toggle on={reasoning} onChange={setReasoning} />
        </label>

        {/* Advanced — Base URL only shown when expanded */}
        <GhostButton
          type="button"
          onClick={() => setShowAdvanced(s => !s)}
          className="self-start flex items-center gap-1.5"
        >
          <span style={{ transform: showAdvanced ? 'rotate(90deg)' : 'rotate(0deg)', transition: 'transform 160ms ease-out', display: 'inline-block' }}>▸</span>
          Advanced
        </GhostButton>
        {showAdvanced && (
          <label className="flex flex-col gap-1.5">
            <span className="text-[11px]" style={{ color: T.textDim }}>Base URL</span>
            <input
              type="text"
              value={baseUrl}
              onChange={e => setBaseUrl(e.target.value)}
              placeholder={provider === 'ollama' ? 'http://localhost:11434' : 'http://localhost:8081/v1'}
              className={inputCls}
              style={inputStyle}
            />
            <span className="text-[11px]" style={{ color: T.textDim }}>
              {provider === 'llamacpp'
                ? 'llama-server OpenAI endpoint. Embeddings still use Ollama.'
                : 'Ollama base URL.'}
            </span>
          </label>
        )}

        <PrimaryButton
          group="identity"
          onClick={() => updateMut.mutate()}
          disabled={updateMut.isPending}
          className="self-start"
        >
          {updateMut.isPending ? 'Saving…' : 'Save'}
        </PrimaryButton>
    </div>
  )
}


// ── Root ─────────────────────────────────────────────────────────────────────

export default function IdentityView() {
  useQueryClient()

  // Fetch saved avatar once; use it as initial state for the local optimistic state.
  const { data: avatarData } = useQuery({
    queryKey: ['agentAvatar'],
    queryFn: () => client.getAgentAvatar(create(GetAgentAvatarRequestSchema, {})),
  })

  const savedAvatar = avatarById(avatarData?.avatarId ?? DEFAULT_AVATAR.id)

  // Local optimistic avatar: updated immediately on swatch click so the viewer
  // re-renders without waiting for the round-trip.
  const [localAvatar, setLocalAvatar] = useState<Avatar | null>(null)
  const currentAvatar = localAvatar ?? savedAvatar

  // When the server-side query settles (or invalidates after a save), sync local
  // state only if no in-flight selection is pending — handled by ModelColumn's
  // onAvatarChange keeping localAvatar up to date, and revert-on-error resetting it.
  // If localAvatar is null we just fall through to savedAvatar above.

  // Expose a stable callback for ModelColumn; captures qc for the error-revert path.
  function handleAvatarChange(av: Avatar) {
    setLocalAvatar(av)
  }

  // Gesture playground cue — stamped with a fresh ts on every click so
  // identical gestures back-to-back still fire (TalkingHeadLib's effect
  // triggers on object-identity change).
  const [previewCue, setPreviewCue] = useState<GestureCue | null>(null)
  function fireCue(cue: Omit<GestureCue, 'ts'>) {
    setPreviewCue({ ts: Date.now(), ...cue })
  }

  // Keep localAvatar in sync once the query hydrates for the first time.
  if (avatarData && localAvatar === null) {
    // This is intentionally a render-time side-effect (set-during-render pattern)
    // because we only want to seed once, not override the user's in-flight choice.
    // localAvatar === null means "not yet seeded" — after this it's always an Avatar.
    // React's rules-of-hooks require we don't call hooks conditionally, but
    // set-state-during-render is fine as long as we only do it when state is null.
    setLocalAvatar(savedAvatar)
  }

  return (
    <div className="flex flex-col gap-5 min-w-0">
      {/* Row A — Names */}
      <RowA />

      {/* Row B — viewer LEFT; avatar / voice / conversation model stacked RIGHT */}
      <div className="grid gap-4" style={{ gridTemplateColumns: 'minmax(0,1fr) 380px' }}>
        <ViewerRow currentAvatar={currentAvatar} gestureCue={previewCue} />
        <div className="flex flex-col gap-4 min-w-0">
          <Card>
            <AvatarRow currentAvatar={currentAvatar} onAvatarChange={handleAvatarChange} />
          </Card>
          <Card>
            <VoiceBar />
          </Card>
          <Card>
            <MicroLabel>Conversation model</MicroLabel>
            <ConversationModelForm />
          </Card>
        </div>
      </div>

      {/* Row C — Gesture playground (full width) */}
      <GesturePlaygroundSection onFire={fireCue} />

      {/* Row D — Advanced (destructive ops) */}
      <details className="rounded-xl border" style={{ borderColor: T.border }}>
        <summary className="cursor-pointer px-5 py-3 text-[12px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>
          Advanced
        </summary>
        <div className="px-5 pb-5 pt-2">
          <ResetAgentStateButton />
        </div>
      </details>

    </div>
  )
}

// ── Reset agent state ────────────────────────────────────────────────────────

function ResetAgentStateButton() {
  const [open, setOpen] = useState(false)
  const [confirmText, setConfirmText] = useState('')
  const [working, setWorking] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const ready = confirmText === 'RESET'

  async function doReset() {
    setWorking(true)
    setErr(null)
    try {
      await client.resetAgentState(create(ResetAgentStateRequestSchema, {}))
      window.location.assign('/onboard')
    } catch (e: any) {
      setErr(e.message ?? String(e))
      setWorking(false)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="text-[12px]" style={{ color: T.textDim }}>
        Reset agent state wipes persons, conversation summaries, tool call log,
        memories, and task findings. Skills, tools, providers, prompts, and
        settings are kept. After reset you'll be redirected to the onboarding
        wizard.
      </p>
      <DangerButton onClick={() => setOpen(true)} className="self-start">Reset agent state…</DangerButton>

      {open && (
        <div className="fixed inset-0 z-50 flex items-center justify-center" style={{ background: 'rgba(0,0,0,0.6)' }}>
          <div className="w-full max-w-md p-5 rounded-xl border" style={{ background: T.surface, borderColor: T.border }}>
            <h3 className="text-[14px] font-semibold mb-2" style={{ color: T.textBright }}>Reset agent state?</h3>
            <p className="text-[12px] mb-4" style={{ color: T.text }}>
              This deletes:
              <br />— all enrolled persons + face embeddings
              <br />— all conversation summaries
              <br />— all tool call history
              <br />— Qdrant memories collection
              <br />— Qdrant task_findings collection
              <br /><br />
              Type <code style={{ color: T.status.error }}>RESET</code> to enable.
            </p>
            <input
              value={confirmText}
              onChange={e => setConfirmText(e.target.value)}
              className="w-full px-3 py-1.5 rounded-lg border bg-transparent text-[13px] mb-3"
              style={{ color: T.textBright, borderColor: T.border }}
              autoFocus
              placeholder="RESET"
            />
            {err && <p className="text-[11px] mb-2" style={{ color: T.status.error }}>{err}</p>}
            <div className="flex justify-end gap-2">
              <SecondaryButton onClick={() => { setOpen(false); setConfirmText('') }}>Cancel</SecondaryButton>
              <DangerButton onClick={doReset} disabled={!ready || working}>
                {working ? 'Wiping…' : 'Confirm reset'}
              </DangerButton>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

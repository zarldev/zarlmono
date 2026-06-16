import { useState, useEffect, useRef } from 'react'
import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { create } from '@bufbuild/protobuf'
import { AdminService, PreviewVoiceRequestSchema, GetVoiceSettingsRequestSchema, EmbedFaceRequestSchema, CompleteOnboardingRequestSchema } from './gen/zarl/v1/admin_pb'
import { T } from './admin-v2/tokens'

const adminClient = createClient(AdminService, createConnectTransport({ baseUrl: window.location.origin }))

type Step =
  | 'welcome'
  | 'agent-name'
  | 'voice'
  | 'model'
  | 'face'
  | 'you'
  | 'family'
  | 'address'
  | 'free-form'
  | 'done'

const ORDER: Step[] = [
  'welcome', 'agent-name', 'voice', 'model', 'face',
  'you', 'family', 'address', 'free-form', 'done',
]

const STEP_TITLES: Record<Step, string> = {
  'welcome':    'Welcome',
  'agent-name': 'Agent name',
  'voice':      'Voice',
  'model':      'Model',
  'face':       'Face captures',
  'you':        'About you',
  'family':     'Household',
  'address':    'Address & currency',
  'free-form':  'Anything else',
  'done':       'Review & finish',
}

export interface FamilyMember {
  name: string
  relationship: 'partner' | 'son' | 'daughter' | 'child'
  dob: string
}

export interface Draft {
  agentName: string
  voiceSpeaker: number
  voiceSpeed: number
  llmModel: string
  faceEmbeddings: Float32Array[]
  facePhotoJpegBase64: string
  personName: string
  personPronouns: string
  personDob: string
  personAddress: string
  personCurrency: string
  family: FamilyMember[]
  freeForm: string
}

const INITIAL_DRAFT: Draft = {
  agentName: 'Zarl',
  voiceSpeaker: 8,
  voiceSpeed: 1.0,
  llmModel: 'qwen3.6-35b-a3b',
  faceEmbeddings: [],
  facePhotoJpegBase64: '',
  personName: '',
  personPronouns: 'he/him',
  personDob: '',
  personAddress: '',
  personCurrency: 'GBP',
  family: [],
  freeForm: '',
}

export default function Onboard({ onNavigate }: { onNavigate: (to: string) => void }) {
  const [step, setStep] = useState<Step>('welcome')
  const [draft, setDraft] = useState<Draft>(INITIAL_DRAFT)
  const update = (patch: Partial<Draft>) => setDraft(d => ({ ...d, ...patch }))

  const idx = ORDER.indexOf(step)
  const next = () => setStep(ORDER[Math.min(idx + 1, ORDER.length - 1)])
  const prev = () => setStep(ORDER[Math.max(idx - 1, 0)])

  return (
    <div className="fixed inset-0 overflow-auto" style={{ background: T.bg, color: T.text }}>
      <div className="max-w-2xl mx-auto px-8 py-12 flex flex-col gap-6">
        <header className="flex items-baseline justify-between">
          <div className="flex flex-col gap-1">
            <span className="op-mono text-[10px] uppercase tracking-[0.16em]" style={{ color: T.accent.identity }}>
              onboarding · wizard
            </span>
            <h1 className="text-[24px] tracking-tight font-semibold" style={{ color: T.textBright }}>
              {STEP_TITLES[step]}
            </h1>
          </div>
          <ProgressPips current={idx} total={ORDER.length} />
        </header>

        {step === 'welcome' && <WelcomeStep onNext={next} />}
        {step === 'agent-name' && <AgentNameStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'voice' && <VoiceStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'model' && <ModelStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'face' && <FaceCaptureStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'you' && <YouStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'family' && <FamilyStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'address' && <AddressStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'free-form' && <FreeFormStep draft={draft} update={update} onNext={next} onBack={prev} />}
        {step === 'done' && <DoneStep draft={draft} onBack={prev} onNavigate={onNavigate} />}
      </div>
    </div>
  )
}

// Shell — op-brackets panel with corner markers and tune-in animation,
// matching the chrome used by Immersive's FloatingPanel and admin-v2 cards.
// Rerunning the key on step change triggers the op-tune-in entrance
// animation so each step fades in instead of swapping cold.
function Shell({ children, step }: { children: React.ReactNode; step: Step }) {
  return (
    <section
      key={step}
      className="op-brackets op-panel op-tune-in relative flex flex-col gap-4 p-6 rounded-sm"
      style={{ background: T.surface, borderColor: T.border }}
    >
      <span className="op-b-tl" aria-hidden />
      <span className="op-b-tr" aria-hidden />
      <span className="op-b-bl" aria-hidden />
      <span className="op-b-br" aria-hidden />
      {children}
    </section>
  )
}

function ProgressPips({ current, total }: { current: number; total: number }) {
  return (
    <div className="flex items-center gap-1.5">
      {Array.from({ length: total }, (_, i) => (
        <span
          key={i}
          aria-hidden
          className="block"
          style={{
            width: i === current ? 14 : 6,
            height: 2,
            background: i <= current ? T.accent.identity : T.border,
            transition: 'width 180ms ease-out, background 180ms ease-out',
          }}
        />
      ))}
      <span className="op-mono text-[10px] ml-2" style={{ color: T.textDim }}>
        {current + 1} / {total}
      </span>
    </div>
  )
}

function WelcomeStep({ onNext }: { onNext: () => void }) {
  return (
    <Shell step="welcome">
      <p className="text-[15px] leading-relaxed" style={{ color: T.text }}>
        This wizard sets up your assistant from scratch — agent name, voice, model,
        face enrolment, and a few personal facts to seed memory.
      </p>
      <p className="text-[12px]" style={{ color: T.textDim }}>
        Takes about three minutes. Closing this tab discards progress.
      </p>
      <div className="flex gap-2 mt-2">
        <PrimaryButton onClick={onNext}>Begin →</PrimaryButton>
      </div>
    </Shell>
  )
}

function AgentNameStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  const valid = draft.agentName.trim().length > 0
  return (
    <Shell step="agent-name">
      <p className="text-[12px]" style={{ color: T.textDim }}>
        What should the assistant call itself?
      </p>
      <input
        autoFocus
        value={draft.agentName}
        onChange={e => update({ agentName: e.target.value })}
        className={fieldCls}
        style={fieldStyle}
        placeholder="Zarl"
      />
      <NavRow onBack={onBack} onNext={onNext} disabled={!valid} />
    </Shell>
  )
}

function VoiceStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  const [numSpeakers, setNumSpeakers] = useState<number>(0)
  const [playing, setPlaying] = useState<number | null>(null)

  useEffect(() => {
    adminClient.getVoiceSettings(create(GetVoiceSettingsRequestSchema, {}))
      .then(r => setNumSpeakers(r.numSpeakers))
      .catch(err => console.error('voice settings:', err))
  }, [])

  async function preview(speaker: number) {
    setPlaying(speaker)
    try {
      const greeting = `Hi, I'm ${draft.agentName.trim() || 'Zarl'}.`
      const r = await adminClient.previewVoice(create(PreviewVoiceRequestSchema, {
        speaker, speed: draft.voiceSpeed, text: greeting,
      }))
      const ctx = new AudioContext({ sampleRate: r.sampleRate })
      const samples = new Int16Array(r.pcm.buffer, r.pcm.byteOffset, r.pcm.byteLength / 2)
      const f32 = new Float32Array(samples.length)
      for (let i = 0; i < samples.length; i++) f32[i] = samples[i] / 32768
      const buf = ctx.createBuffer(1, f32.length, r.sampleRate)
      buf.getChannelData(0).set(f32)
      const src = ctx.createBufferSource()
      src.buffer = buf
      src.connect(ctx.destination)
      src.onended = () => { ctx.close(); setPlaying(null) }
      src.start()
    } catch (err) {
      console.error('preview failed:', err)
      setPlaying(null)
    }
  }

  return (
    <Shell step="voice">
      <p className="text-[12px]" style={{ color: T.textDim }}>Click a row to hear it.</p>
      <div className="flex flex-col gap-1.5 max-h-[320px] overflow-y-auto">
        {Array.from({ length: numSpeakers }, (_, i) => {
          const selected = draft.voiceSpeaker === i
          return (
            <button
              key={i}
              onClick={() => { update({ voiceSpeaker: i }); preview(i) }}
              className="flex items-center gap-3 px-3 py-2 rounded-sm border text-left text-[13px] transition-colors"
              style={{
                borderColor: selected ? T.accent.identity : T.border,
                background: selected ? `${T.accent.identity}14` : 'transparent',
                color: T.text,
              }}
            >
              <span className="op-mono text-[11px] w-8" style={{ color: selected ? T.accent.identity : T.textDim }}>
                #{i.toString().padStart(2, '0')}
              </span>
              <span className="flex-1">Speaker {i}</span>
              {playing === i && (
                <span className={`op-eq is-playing`} aria-hidden>
                  <span /><span /><span />
                </span>
              )}
            </button>
          )
        })}
      </div>
      <label className="flex items-center gap-3 text-[12px]" style={{ color: T.text }}>
        <span className="op-mono text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>Speed</span>
        <input
          type="range" min="0.7" max="1.4" step="0.05"
          value={draft.voiceSpeed}
          onChange={e => update({ voiceSpeed: parseFloat(e.target.value) })}
          className="flex-1 accent-current"
          style={{ accentColor: T.accent.identity } as React.CSSProperties}
        />
        <span className="op-mono text-[11px] w-12 text-right tabular-nums" style={{ color: T.textBright }}>
          {draft.voiceSpeed.toFixed(2)}×
        </span>
      </label>
      <NavRow onBack={onBack} onNext={onNext} />
    </Shell>
  )
}

const KNOWN_MODELS = [
  { id: 'qwen3.6-35b-a3b',   label: 'Qwen3.6 35B-A3B — local llama.cpp (default)' },
  { id: 'qwen3.6-27b',        label: 'Qwen3.6 27B dense' },
  { id: 'gpt-oss-20b',        label: 'GPT-OSS 20B' },
  { id: 'llama-3.1-8b',       label: 'Llama 3.1 8B' },
]

function ModelStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  return (
    <Shell step="model">
      <p className="text-[12px]" style={{ color: T.textDim }}>
        Sets the conversation LLM model only. Provider, base URL, and API key keep their
        existing values — change them later in Admin → Task runner.
      </p>
      <div className="flex flex-col gap-1.5">
        {KNOWN_MODELS.map(m => {
          const selected = draft.llmModel === m.id
          return (
            <button
              key={m.id}
              onClick={() => update({ llmModel: m.id })}
              className="flex items-center gap-3 px-3 py-2 rounded-sm border text-left text-[13px] transition-colors"
              style={{
                borderColor: selected ? T.accent.identity : T.border,
                background: selected ? `${T.accent.identity}14` : 'transparent',
              }}
            >
              <span className="op-mono text-[11px]" style={{ color: selected ? T.accent.identity : T.textDim }}>
                {m.id}
              </span>
              <span className="flex-1" style={{ color: T.text }}>{m.label}</span>
            </button>
          )
        })}
      </div>
      <NavRow onBack={onBack} onNext={onNext} />
    </Shell>
  )
}

const POSE_PROMPTS = [
  'Look straight at the camera',
  'Turn slightly to your left',
  'Turn slightly to your right',
] as const

function FaceCaptureStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const [poseIdx, setPoseIdx] = useState(0)
  const [capturing, setCapturing] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    navigator.mediaDevices.getUserMedia({ video: { width: 640, height: 480 }, audio: false })
      .then(s => {
        if (cancelled) { s.getTracks().forEach(t => t.stop()); return }
        streamRef.current = s
        if (videoRef.current) {
          videoRef.current.srcObject = s
          videoRef.current.play().catch(() => {})
        }
      })
      .catch(err => setError(`camera: ${err.message}`))
    return () => {
      cancelled = true
      streamRef.current?.getTracks().forEach(t => t.stop())
      streamRef.current = null
    }
  }, [])

  async function capture() {
    if (!videoRef.current) return
    setCapturing(true)
    setError(null)
    try {
      const v = videoRef.current
      const canvas = document.createElement('canvas')
      canvas.width = v.videoWidth
      canvas.height = v.videoHeight
      const ctx = canvas.getContext('2d')!
      ctx.drawImage(v, 0, 0)
      const blob = await new Promise<Blob | null>(r => canvas.toBlob(r, 'image/jpeg', 0.9))
      if (!blob) throw new Error('canvas.toBlob returned null')
      const jpeg = new Uint8Array(await blob.arrayBuffer())
      const r = await adminClient.embedFace(create(EmbedFaceRequestSchema, { jpeg }))
      if (!r.embedding) throw new Error('no embedding returned')
      const embs = [...draft.faceEmbeddings, new Float32Array(r.embedding.values)]
      update({
        faceEmbeddings: embs,
        facePhotoJpegBase64: poseIdx === 0 ? r.photoJpegBase64 : draft.facePhotoJpegBase64,
      })
      if (poseIdx + 1 < POSE_PROMPTS.length) {
        setPoseIdx(poseIdx + 1)
      }
    } catch (err: any) {
      setError(err.message ?? String(err))
    } finally {
      setCapturing(false)
    }
  }

  function retake() {
    if (draft.faceEmbeddings.length === 0) return
    const lastIdx = draft.faceEmbeddings.length - 1
    update({ faceEmbeddings: draft.faceEmbeddings.slice(0, lastIdx) })
    setPoseIdx(lastIdx)
  }

  const canAdvance = draft.faceEmbeddings.length === POSE_PROMPTS.length

  return (
    <Shell step="face">
      <div className="flex items-center gap-2">
        <span className="op-mono text-[10px] uppercase tracking-[0.12em]" style={{ color: T.accent.identity }}>
          pose {Math.min(poseIdx + 1, POSE_PROMPTS.length)} / {POSE_PROMPTS.length}
        </span>
        <span style={{ color: T.textDim }}>·</span>
        <span className="text-[13px]" style={{ color: T.text }}>{POSE_PROMPTS[poseIdx]}</span>
      </div>
      <div className="relative self-start">
        <video
          ref={videoRef}
          muted
          playsInline
          className="block rounded-sm bg-black w-full max-w-[480px] -scale-x-100 border"
          style={{ borderColor: T.border }}
        />
        <div className="op-scanlines absolute inset-0 pointer-events-none rounded-sm" aria-hidden />
        <div className="op-crosshair" aria-hidden>
          <span className="op-crosshair-box" />
        </div>
        <div className="flex gap-1 absolute top-2 right-2" aria-hidden>
          {POSE_PROMPTS.map((_, i) => (
            <span
              key={i}
              className="block"
              style={{
                width: 10,
                height: 10,
                borderRadius: '50%',
                background: i < draft.faceEmbeddings.length ? T.status.ok : 'rgba(0,0,0,0.45)',
                border: `1px solid ${i < draft.faceEmbeddings.length ? T.status.ok : T.border}`,
              }}
            />
          ))}
        </div>
      </div>
      {error && (
        <p className="op-mono text-[11px]" style={{ color: T.status.error }}>{error}</p>
      )}
      <div className="flex items-center gap-3 flex-wrap">
        <span className="op-mono text-[11px]" style={{ color: T.textDim }}>
          captured {draft.faceEmbeddings.length} / {POSE_PROMPTS.length}
        </span>
        {!canAdvance && (
          <SecondaryButton onClick={capture} disabled={capturing}>
            {capturing ? 'capturing…' : 'Capture'}
          </SecondaryButton>
        )}
        {draft.faceEmbeddings.length > 0 && (
          <GhostButton onClick={retake}>Retake last</GhostButton>
        )}
      </div>
      <NavRow onBack={onBack} onNext={onNext} disabled={!canAdvance} />
    </Shell>
  )
}

function YouStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  const valid = draft.personName.trim().length > 0 && draft.personDob.length > 0
  return (
    <Shell step="you">
      <Field label="Name">
        <input value={draft.personName} onChange={e => update({ personName: e.target.value })}
          className={fieldCls} style={fieldStyle} placeholder="e.g. Alex" autoFocus />
      </Field>
      <Field label="Pronouns">
        <select value={draft.personPronouns} onChange={e => update({ personPronouns: e.target.value })}
          className={fieldCls} style={fieldStyle}>
          <option>he/him</option>
          <option>she/her</option>
          <option>they/them</option>
          <option>other</option>
        </select>
      </Field>
      <Field label="Date of birth">
        <input type="date" value={draft.personDob} onChange={e => update({ personDob: e.target.value })}
          className={fieldCls} style={fieldStyle} />
      </Field>
      <NavRow onBack={onBack} onNext={onNext} disabled={!valid} />
    </Shell>
  )
}

function FamilyStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  function add() {
    update({ family: [...draft.family, { name: '', relationship: 'partner', dob: '' }] })
  }
  function patch(i: number, p: Partial<FamilyMember>) {
    const next = draft.family.slice()
    next[i] = { ...next[i], ...p }
    update({ family: next })
  }
  function remove(i: number) {
    update({ family: draft.family.filter((_, j) => j !== i) })
  }
  return (
    <Shell step="family">
      <p className="text-[12px]" style={{ color: T.textDim }}>
        Partner + each child. Skip if none — empty rows are dropped.
      </p>
      {draft.family.map((m, i) => (
        <div
          key={i}
          className="flex flex-wrap items-end gap-2 rounded-sm border p-3"
          style={{ borderColor: T.border, background: T.raised }}
        >
          <Field label="Name">
            <input value={m.name} onChange={e => patch(i, { name: e.target.value })}
              className={fieldCls} style={fieldStyle} />
          </Field>
          <Field label="Relationship">
            <select value={m.relationship}
              onChange={e => patch(i, { relationship: e.target.value as FamilyMember['relationship'] })}
              className={fieldCls} style={fieldStyle}>
              <option value="partner">partner</option>
              <option value="son">son</option>
              <option value="daughter">daughter</option>
              <option value="child">child (other)</option>
            </select>
          </Field>
          <Field label="DOB">
            <input type="date" value={m.dob} onChange={e => patch(i, { dob: e.target.value })}
              className={fieldCls} style={fieldStyle} />
          </Field>
          <button
            onClick={() => remove(i)}
            className="op-mono text-[10px] uppercase tracking-[0.12em] px-2 py-1.5 rounded-sm border"
            style={{ color: T.status.error, borderColor: `${T.status.error}40` }}
          >Remove</button>
        </div>
      ))}
      <GhostButton onClick={add}>+ Add member</GhostButton>
      <NavRow onBack={onBack} onNext={onNext} />
    </Shell>
  )
}

function AddressStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  return (
    <Shell step="address">
      <Field label="Home address">
        <input value={draft.personAddress} onChange={e => update({ personAddress: e.target.value })}
          className={fieldCls} style={fieldStyle} placeholder="123 Example Street, AB1 2CD" />
      </Field>
      <Field label="Currency">
        <select value={draft.personCurrency} onChange={e => update({ personCurrency: e.target.value })}
          className={fieldCls} style={fieldStyle}>
          <option value="GBP">GBP — pounds sterling</option>
          <option value="EUR">EUR — euros</option>
          <option value="USD">USD — US dollars</option>
        </select>
      </Field>
      <NavRow onBack={onBack} onNext={onNext} />
    </Shell>
  )
}

function FreeFormStep({ draft, update, onNext, onBack }: {
  draft: Draft
  update: (p: Partial<Draft>) => void
  onNext: () => void
  onBack: () => void
}) {
  return (
    <Shell step="free-form">
      <p className="text-[12px]" style={{ color: T.textDim }}>
        One fact per line. Each non-empty line becomes its own memory.
      </p>
      <textarea
        value={draft.freeForm}
        onChange={e => update({ freeForm: e.target.value })}
        className={`${fieldCls} min-h-[140px] font-mono text-[12px]`}
        style={fieldStyle}
        placeholder={`Vegetarian\nPrefers terse answers\nWorks remotely`}
      />
      <NavRow onBack={onBack} onNext={onNext} />
    </Shell>
  )
}

const fieldCls = 'px-3 py-2 rounded-sm border bg-transparent text-[13px] outline-none w-full max-w-[420px]'
const fieldStyle: React.CSSProperties = { color: T.textBright, borderColor: T.border }

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="op-mono text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>
        {label}
      </span>
      {children}
    </label>
  )
}

function NavRow({ onBack, onNext, disabled }: { onBack: () => void; onNext: () => void; disabled?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-2 mt-2 pt-3 border-t" style={{ borderColor: T.border }}>
      <GhostButton onClick={onBack}>← Back</GhostButton>
      <PrimaryButton onClick={onNext} disabled={disabled}>Next →</PrimaryButton>
    </div>
  )
}

// Buttons — three levels. PrimaryButton uses the Identity accent so the
// wizard reads as an Identity-group action. SecondaryButton is a neutral
// bordered control for in-step actions like Capture. GhostButton is flat
// text for back / cancel / add-row affordances.
function PrimaryButton({ onClick, disabled, children }: { onClick: () => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="op-mono text-[11px] uppercase tracking-[0.14em] px-4 py-1.5 rounded-sm border transition-colors disabled:opacity-40"
      style={{
        color: T.accent.identity,
        borderColor: `${T.accent.identity}66`,
        background: `${T.accent.identity}14`,
      }}
    >{children}</button>
  )
}

function SecondaryButton({ onClick, disabled, children }: { onClick: () => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="op-mono text-[11px] uppercase tracking-[0.14em] px-3 py-1.5 rounded-sm border transition-colors disabled:opacity-40"
      style={{ color: T.textBright, borderColor: T.borderStrong }}
    >{children}</button>
  )
}

function GhostButton({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className="op-mono text-[11px] uppercase tracking-[0.14em] px-3 py-1.5 rounded-sm transition-colors"
      style={{ color: T.textDim }}
    >{children}</button>
  )
}

const CURRENCY_NAMES: Record<string, string> = {
  GBP: 'pounds sterling',
  EUR: 'euros',
  USD: 'US dollars',
}

function buildBullets(d: Draft): string[] {
  const name = d.personName.trim()
  if (!name) return []
  const out: string[] = []
  out.push(`${name} uses ${d.personPronouns} pronouns.`)
  if (d.personDob) out.push(`${name} was born on ${d.personDob}.`)
  if (d.personAddress.trim()) out.push(`${name}'s home address is ${d.personAddress.trim()}.`)
  if (d.personCurrency) out.push(`${name} wants all costings in ${CURRENCY_NAMES[d.personCurrency] ?? d.personCurrency}.`)
  for (const m of d.family) {
    if (!m.name.trim()) continue
    const article = m.relationship === 'partner' ? 'partner' : m.relationship
    const dobClause = m.dob ? `, born ${m.dob}` : ''
    if (m.relationship === 'partner') {
      out.push(`${name}'s partner is ${m.name.trim()}${dobClause}.`)
    } else {
      out.push(`${name}'s ${article} is ${m.name.trim()}${dobClause}.`)
    }
  }
  for (const line of d.freeForm.split('\n')) {
    const t = line.trim()
    if (t) out.push(t)
  }
  return out
}

function DoneStep({ draft, onBack, onNavigate }: {
  draft: Draft
  onBack: () => void
  onNavigate: (to: string) => void
}) {
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const bullets = buildBullets(draft)

  async function finish() {
    setSubmitting(true)
    setError(null)
    try {
      const req = create(CompleteOnboardingRequestSchema, {
        agentName: draft.agentName.trim(),
        voiceSpeaker: draft.voiceSpeaker,
        voiceSpeed: draft.voiceSpeed,
        llmModel: draft.llmModel,
        personName: draft.personName.trim(),
        personPronouns: draft.personPronouns,
        personDob: draft.personDob,
        personAddress: draft.personAddress.trim(),
        personCurrency: draft.personCurrency,
        family: draft.family
          .filter(m => m.name.trim())
          .map(m => ({ name: m.name.trim(), relationship: m.relationship, dob: m.dob })),
        freeFormFacts: bullets,
        faceEmbeddings: draft.faceEmbeddings.map(e => ({ values: Array.from(e) })),
        facePhotoJpegBase64: draft.facePhotoJpegBase64,
      })
      await adminClient.completeOnboarding(req)
      onNavigate('/')
    } catch (err: any) {
      setError(err.message ?? String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Shell step="done">
      <Summary label="Agent name" value={draft.agentName} />
      <Summary label="Voice" value={`speaker ${draft.voiceSpeaker} @ ${draft.voiceSpeed.toFixed(2)}×`} />
      <Summary label="Model" value={draft.llmModel} />
      <Summary label="Face poses captured" value={`${draft.faceEmbeddings.length}`} />
      <Summary label="Memory bullets" value={`${bullets.length}`} />
      <details className="text-[12px]" style={{ color: T.textDim }}>
        <summary className="cursor-pointer op-mono uppercase tracking-[0.12em] text-[10px]">Show bullets</summary>
        <ul className="mt-2 flex flex-col gap-1 pl-4 list-disc">
          {bullets.map((b, i) => <li key={i} style={{ color: T.text }}>{b}</li>)}
        </ul>
      </details>
      {error && (
        <p className="op-mono text-[11px]" style={{ color: T.status.error }}>{error}</p>
      )}
      <div className="flex items-center justify-between gap-2 mt-2 pt-3 border-t" style={{ borderColor: T.border }}>
        <GhostButton onClick={onBack}>← Back</GhostButton>
        <PrimaryButton onClick={finish} disabled={submitting}>
          {submitting ? 'Saving…' : 'Finish →'}
        </PrimaryButton>
      </div>
    </Shell>
  )
}

function Summary({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between items-baseline py-1.5 border-b" style={{ borderColor: T.border }}>
      <span className="op-mono text-[10px] uppercase tracking-[0.12em]" style={{ color: T.textDim }}>
        {label}
      </span>
      <span className="text-[13px]" style={{ color: T.textBright }}>{value}</span>
    </div>
  )
}

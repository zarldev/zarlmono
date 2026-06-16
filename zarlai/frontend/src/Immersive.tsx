import { useState, useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { create } from '@bufbuild/protobuf'
import { usePresenceSession } from './hooks/usePresenceSession'
import TalkingHeadLib from './TalkingHeadLib'
import ParticleField from './components/ParticleField'
import StateGlow from './components/StateGlow'
import StateMonitor from './components/StateMonitor'
import { FloatingNowPlaying } from './components/FloatingNowPlaying'
import FloatingChart from './components/FloatingChart'
import FloatingFindings from './components/FloatingFindings'
import FloatingReport from './components/FloatingReport'
import ThinkingPanel from './components/ThinkingPanel'
import ToolCallsPanel from './components/ToolCallsPanel'
import HiddenTextInput from './components/HiddenTextInput'
import { FloatingPanel } from './components/FloatingPanel'
import { DEFAULT_AVATAR, avatarById, type Avatar } from './avatars'
import { client as adminClient } from './admin/shared'
import { GetAgentAvatarRequestSchema } from './gen/zarl/v1/admin_pb'

const AVATAR_STORAGE_KEY = 'zarl.avatar'

interface ImmersiveProps { onNavigate?: (to: string) => void }

async function fileToJpegBytes(file: File): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => {
      const canvas = document.createElement('canvas')
      const scale = Math.min(1, 640 / img.width)
      canvas.width = Math.round(img.width * scale)
      canvas.height = Math.round(img.height * scale)
      const ctx = canvas.getContext('2d')
      if (!ctx) {
        reject(new Error('canvas 2d context unavailable'))
        return
      }
      ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
      const dataUrl = canvas.toDataURL('image/jpeg', 0.7)
      const bin = atob(dataUrl.split(',')[1])
      const bytes = new Uint8Array(bin.length)
      for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
      resolve(bytes)
      URL.revokeObjectURL(img.src)
    }
    img.onerror = () => {
      URL.revokeObjectURL(img.src)
      reject(new Error('failed to load image'))
    }
    img.src = URL.createObjectURL(file)
  })
}

export default function Immersive({ onNavigate }: ImmersiveProps) {
  const s = usePresenceSession()
  // scene is null: TalkingHeadLib owns its own renderer/scene and doesn't expose it.
  // ParticleField gracefully handles null and renders nothing.
  const [controlsVisible, setControlsVisible] = useState(true)
  const [showText, setShowText] = useState(false)
  const [showThinking, setShowThinking] = useState(false)
  const [showToolCalls, setShowToolCalls] = useState(false)
  const [showCamera, setShowCamera] = useState(false)
  const cameraVideoRef = useRef<HTMLVideoElement | null>(null)
  const [cameraHover, setCameraHover] = useState(false)
  const [cameraTimecode, setCameraTimecode] = useState<string>(() => new Date().toLocaleTimeString([], { hour12: false }))
  // Avatar source-of-truth is the server (set on the Identity admin page).
  // localStorage is a per-device cache so the right model paints immediately on
  // load, before the server query resolves — and so the in-conversation
  // overlay picker keeps working offline-ish.
  const [avatar, setAvatar] = useState<Avatar>(() => {
    const stored = typeof window !== 'undefined' ? window.localStorage.getItem(AVATAR_STORAGE_KEY) : null
    return stored ? avatarById(stored) : DEFAULT_AVATAR
  })

  // Server query — when it settles, override the local cache with the
  // server's value so a fresh Identity-page change wins on next mount.
  const { data: serverAvatar } = useQuery({
    queryKey: ['agentAvatar'],
    queryFn: () => adminClient.getAgentAvatar(create(GetAgentAvatarRequestSchema, {})),
  })
  useEffect(() => {
    const id = serverAvatar?.avatarId
    if (!id) return
    const next = avatarById(id)
    if (next.id !== avatar.id) {
      setAvatar(next)
      window.localStorage.setItem(AVATAR_STORAGE_KEY, next.id)
    }
  }, [serverAvatar?.avatarId])

  const hideTimer = useRef<number>(0)

  useEffect(() => {
    function onMove() {
      setControlsVisible(true)
      window.clearTimeout(hideTimer.current)
      hideTimer.current = window.setTimeout(() => setControlsVisible(false), 3000)
    }
    onMove()
    window.addEventListener('mousemove', onMove)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.clearTimeout(hideTimer.current)
    }
  }, [])

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement
      const inField = target.tagName === 'INPUT' || target.tagName === 'TEXTAREA'

      if (e.key === 'Escape') {
        if (s.pendingReport) s.dismissReport()
        else if (s.pendingChart) s.dismissChart()
        else if (s.pendingFindings) s.dismissFindings()
        else if (showText) setShowText(false)
        return
      }
      if (inField) return

      if (e.key === 't' || e.key === 'T' || e.key === '/') {
        if (!showText) {
          e.preventDefault()
          setShowText(true)
        }
      } else if (e.key === 'm' || e.key === 'M') {
        s.setMuted(!s.muted)
      } else if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault()
        window.open('/admin', '_blank', 'noopener')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [showText, s.pendingChart, s.muted, onNavigate]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    async function onDrop(e: DragEvent) {
      e.preventDefault()
      const files = Array.from(e.dataTransfer?.files ?? []).filter(f => f.type.startsWith('image/'))
      for (const f of files) {
        try {
          const bytes = await fileToJpegBytes(f)
          s.attachImage(bytes)
        } catch (err) {
          console.warn('image attach failed:', err)
        }
      }
    }
    async function onPaste(e: ClipboardEvent) {
      const items = Array.from(e.clipboardData?.items ?? [])
      const imageItems = items.filter(it => it.type.startsWith('image/'))
      if (!imageItems.length) return
      e.preventDefault()
      const files = imageItems.map(it => it.getAsFile()).filter(Boolean) as File[]
      for (const f of files) {
        try {
          const bytes = await fileToJpegBytes(f)
          s.attachImage(bytes)
        } catch (err) {
          console.warn('image attach failed:', err)
        }
      }
    }
    function onDragOver(e: DragEvent) { e.preventDefault() }
    window.addEventListener('drop', onDrop)
    window.addEventListener('dragover', onDragOver)
    window.addEventListener('paste', onPaste)
    return () => {
      window.removeEventListener('drop', onDrop)
      window.removeEventListener('dragover', onDragOver)
      window.removeEventListener('paste', onPaste)
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const el = cameraVideoRef.current
    if (!el) return
    el.srcObject = showCamera ? s.mediaStream : null
  }, [showCamera, s.mediaStream])

  useEffect(() => {
    if (!showCamera) return
    const id = window.setInterval(() => {
      setCameraTimecode(new Date().toLocaleTimeString([], { hour12: false }))
    }, 1000)
    return () => window.clearInterval(id)
  }, [showCamera])

  const effectiveVisible = controlsVisible && s.state !== 'speaking'

  return (
    <div className="fixed inset-0 bg-[#07090f] text-[#c8cad0] overflow-hidden">
      <StateGlow state={s.state} analyser={s.ttsAnalyser} />
      <TalkingHeadLib analyser={s.ttsAnalyser} state={s.state} avatar={avatar} gestureCue={s.gestureCue} />
      <ParticleField scene={null} state={s.state} />
      <div
        className={`fixed bottom-6 left-1/2 z-20 -translate-x-1/2 transition-opacity duration-300 ${effectiveVisible ? 'opacity-100' : 'opacity-35'}`}
      >
        <StateMonitor state={s.state} ttsAnalyser={s.ttsAnalyser} micAnalyser={s.micAnalyser} />
      </div>
      <FloatingNowPlaying info={s.nowPlaying} isOpen={s.nowPlayingOpen} onClose={s.dismissNowPlaying} />
      <div
        className={`fixed top-4 left-4 z-30 op-rail transition-opacity duration-300 ${effectiveVisible ? 'opacity-100' : 'opacity-35 pointer-events-none'}`}
        aria-label="Instrumentation"
      >
        <button
          onClick={() => s.setMuted(!s.muted)}
          className="op-slot group"
          data-active={!s.muted}
          title={s.muted ? 'Unmute (M)' : 'Mute (M)'}
          aria-label={s.muted ? 'Unmute' : 'Mute'}
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            {s.muted ? (
              <>
                <line x1="1" y1="1" x2="23" y2="23" />
                <path d="M9 9v3a3 3 0 0 0 5.12 2.12M15 9.34V4a3 3 0 0 0-5.94-.6" />
                <path d="M17 16.95A7 7 0 0 1 5 12v-2m14 0v2c0 .74-.11 1.46-.33 2.13" />
                <line x1="12" y1="19" x2="12" y2="23" /><line x1="8" y1="23" x2="16" y2="23" />
              </>
            ) : (
              <>
                <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z" />
                <path d="M19 10v2a7 7 0 0 1-14 0v-2" />
                <line x1="12" y1="19" x2="12" y2="23" /><line x1="8" y1="23" x2="16" y2="23" />
              </>
            )}
          </svg>
          <span className="op-caption">{s.muted ? 'muted' : 'mic'}</span>
        </button>

        <span className="op-tick" aria-hidden />

        <button
          onClick={() => setShowCamera((v) => !v)}
          className="op-slot group"
          data-active={showCamera}
          title={showCamera ? 'Hide camera preview' : 'Show camera preview'}
          aria-label="Toggle camera preview"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z" />
            <circle cx="12" cy="13" r="4" />
            {!showCamera && <line x1="2" y1="2" x2="22" y2="22" />}
          </svg>
          <span className="op-caption">{showCamera ? 'camera' : 'camera off'}</span>
        </button>

        <span className="op-tick" aria-hidden />

        <button
          onClick={() => setShowText((v) => !v)}
          className="op-slot group"
          data-active={showText}
          title="Type to Zarl (T)"
          aria-label="Toggle text input"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
          </svg>
          <span className="op-caption">text</span>
        </button>

        <span className="op-tick" aria-hidden />

        <button
          onClick={() => setShowThinking((v) => !v)}
          className="op-slot group"
          data-active={showThinking}
          title={showThinking ? 'Hide thinking' : 'Show thinking'}
          aria-label="Toggle thinking"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M9.663 17h4.673M12 3v1m0 16v1m8-9h1M3 12h1m14.364-6.364l-.707.707M5.343 18.657l.707-.707M18.364 18.364l-.707-.707M5.636 5.636l-.707.707" />
            <circle cx="12" cy="12" r="4" />
          </svg>
          <span className="op-caption">thinking</span>
        </button>

        <span className="op-tick" aria-hidden />

        <button
          onClick={() => setShowToolCalls((v) => !v)}
          className="op-slot group"
          data-active={showToolCalls}
          title={showToolCalls ? 'Hide tool calls' : 'Show tool calls'}
          aria-label="Toggle tool calls"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
          </svg>
          <span className="op-caption">tools</span>
        </button>

        <span className="op-tick" aria-hidden />

        <button
          onClick={() => window.open('/admin', '_blank', 'noopener')}
          className="op-slot group"
          title="Admin (Cmd+K)"
          aria-label="Settings"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
          <span className="op-caption">settings</span>
          {s.pendingProposals > 0 && (
            <span className="absolute top-0 right-0 w-1.5 h-1.5 rounded-full bg-[#f59e0b] op-blink" aria-hidden />
          )}
        </button>
      </div>

      <ThinkingPanel entry={s.latestThinking} isOpen={showThinking} onClose={() => setShowThinking(false)} />
      <ToolCallsPanel calls={s.toolCalls} isOpen={showToolCalls} onClose={() => setShowToolCalls(false)} />
      <FloatingPanel
        id="camera"
        title="cam 01"
        isOpen={showCamera}
        onClose={() => setShowCamera(false)}
        defaultCorner="br"
        width={256}
        accessory={
          <span className="flex items-center gap-1">
            <span className="w-1.5 h-1.5 rounded-full bg-[#f59e0b] op-blink" aria-hidden />
            <span className="text-[#f59e0b]">live</span>
          </span>
        }
        footer={
          <>
            <span>feed · raw</span>
            <span className="text-[#f59e0b]/80 tabular-nums">{cameraTimecode}</span>
          </>
        }
      >
        <div
          className="relative bg-black"
          onMouseEnter={() => setCameraHover(true)}
          onMouseLeave={() => setCameraHover(false)}
        >
          <video
            ref={cameraVideoRef}
            autoPlay
            playsInline
            muted
            className="block w-full h-auto -scale-x-100"
          />
          <div className="op-scanlines absolute inset-0 pointer-events-none" aria-hidden />
          <div className="op-vignette absolute inset-0 pointer-events-none" aria-hidden />
          <div
            className="op-crosshair"
            style={{ opacity: cameraHover ? 0 : 0.6 }}
            aria-hidden
          >
            <span className="op-crosshair-box" />
          </div>
        </div>
      </FloatingPanel>
      <FloatingChart spec={s.pendingChart} onDismiss={s.dismissChart} />
      <FloatingFindings spec={s.pendingFindings} onDismiss={s.dismissFindings} />
      <FloatingReport spec={s.pendingReport} onDismiss={s.dismissReport} />
      <HiddenTextInput
        visible={showText}
        onSend={(t) => { s.send(t) }}
        onDismiss={() => setShowText(false)}
        onAttachFiles={async (files) => {
          let added = 0
          for (const f of files) {
            try {
              const bytes = await fileToJpegBytes(f)
              s.attachImage(bytes)
              added++
            } catch (err) {
              console.warn('image attach failed:', err)
            }
          }
          return added
        }}
      />
    </div>
  )
}

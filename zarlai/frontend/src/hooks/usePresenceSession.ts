import { useState, useRef, useEffect, useCallback } from 'react'
import { dHash, hammingSimilarity } from '../imageHash'
import {
  sendText as rpcSendText,
  sendAudio as rpcSendAudio,
  subscribeNotifications,
  type ConverseCallbacks,
  type LocationInfo,
} from './useConverse'
import { createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { create } from '@bufbuild/protobuf'
import { AdminService, ListToolProposalsRequestSchema, ListPromptProposalsRequestSchema, ListToolCallsRequestSchema } from '@/gen/zarl/v1/admin_pb'
import type { NowPlayingInfo } from '../components/NowPlaying'

export type SessionState = 'loading' | 'listening' | 'processing' | 'speaking'

export type ChartPoint = { x: string; y: number }
export type ChartSeries = { name: string; points: ChartPoint[] }
export type ChartSpec = {
  title: string
  x_label?: string
  y_label?: string
  y_min?: number
  y_max?: number
  y_zero_based?: boolean
  series: ChartSeries[]
}

export type FindingItem = {
  title: string
  url: string
  summary?: string
  source?: string
}
export type FindingsSpec = {
  title: string
  items: FindingItem[]
}

export type ReportSpec = {
  task_id: string
  title: string
  markdown: string
  obsidian_path?: string
  person_name?: string
}

// A thinking event is the model's internal reasoning from the most recent
// turn. The hook keeps the latest one around; the UI chooses whether to
// display it.
export interface ThinkingEntry {
  at: number
  content: string
}

// Reactive gesture/mood cues. The hook emits one whenever something notable
// happens (task complete, chart rendered, error…) and the TalkingHead
// component consumes the cue once.
export type GestureName =
  | 'handup' | 'index' | 'ok' | 'thumbup' | 'thumbdown'
  | 'side' | 'shrug' | 'namaste'
  // Custom templates (defined in talkingHeadGestures.ts)
  | 'wave' | 'peace' | 'stop' | 'pointself' | 'fistpump' | 'beckon'
export type MoodName =
  | 'neutral' | 'happy' | 'angry' | 'sad' | 'fear' | 'disgust' | 'love' | 'sleep'
export interface GestureCue {
  ts: number
  gesture?: GestureName
  mood?: MoodName
  moodHoldMs?: number
}


export interface ToolLogEntry {
  time: string
  tool: string
  status: string
  summary: string
}

// ToolCallEntry is the persisted detail-level row — fetched from
// AdminService.ListToolCalls and filtered to the current session. The
// ToolCallsPanel renders it. ToolLogEntry is the ephemeral status-stream
// entry displayed in the existing small "logs" strip — both coexist
// because they serve different UIs at different granularities.
export interface ToolCallEntry {
  at: number        // epoch millis for sort + display
  name: string
  provider: string
  duration_ms: number
  args?: string
  result?: string
  error?: string
}

export interface PresenceSession {
  state: SessionState
  ttsAnalyser: AnalyserNode | null
  micAnalyser: AnalyserNode | null
  sessionId: string
  send: (text: string, images?: Uint8Array[]) => Promise<void>
  stopSpeaking: () => void
  muted: boolean
  setMuted: (muted: boolean) => void
  pendingChart: ChartSpec | null
  dismissChart: () => void
  pendingFindings: FindingsSpec | null
  dismissFindings: () => void
  pendingReport: ReportSpec | null
  dismissReport: () => void
  latestThinking: ThinkingEntry | null
  gestureCue: GestureCue | null
  pendingProposals: number
  logs: ToolLogEntry[]
  toolCalls: ToolCallEntry[]
  attachImage: (jpeg: Uint8Array) => void
  mediaStream: MediaStream | null
  nowPlaying: NowPlayingInfo | null
  nowPlayingOpen: boolean
  dismissNowPlaying: () => void
}

const adminClient = createClient(AdminService, createConnectTransport({ baseUrl: window.location.origin }))

export function usePresenceSession(): PresenceSession {
  const [state, setState] = useState<SessionState>('loading')
  const [muted, setMutedState] = useState(false)
  const [pendingChart, setPendingChart] = useState<ChartSpec | null>(null)
  const [pendingFindings, setPendingFindings] = useState<FindingsSpec | null>(null)
  const [pendingReport, setPendingReport] = useState<ReportSpec | null>(null)
  const [nowPlaying, setNowPlaying] = useState<NowPlayingInfo | null>(null)
  const [nowPlayingOpen, setNowPlayingOpen] = useState(true)
  const [latestThinking, setLatestThinking] = useState<ThinkingEntry | null>(null)
  const [gestureCue, setGestureCue] = useState<GestureCue | null>(null)
  const emitGesture = useCallback((cue: Omit<GestureCue, 'ts'>) => {
    setGestureCue({ ts: Date.now(), ...cue })
  }, [])
  const [pendingProposals, setPendingProposals] = useState(0)
  const [logs, setLogs] = useState<ToolLogEntry[]>([])
  const [toolCalls, setToolCalls] = useState<ToolCallEntry[]>([])
  const [ttsAnalyser, setTtsAnalyser] = useState<AnalyserNode | null>(null)
  const [micAnalyser, setMicAnalyser] = useState<AnalyserNode | null>(null)
  const [sessionId, setSessionId] = useState('')

  // refs-that-shadow-state for stable callback access
  const stateRef = useRef<SessionState>('loading')
  const mutedRef = useRef(false)
  const sessionIdRef = useRef('')
  const mediaStreamRef = useRef<MediaStream | null>(null)
  const [mediaStream, setMediaStream] = useState<MediaStream | null>(null)
  const vadRef = useRef<any>(null)
  const audioCtxRef = useRef<AudioContext | null>(null)
  const ttsAnalyserRef = useRef<AnalyserNode | null>(null)
  const nextTimeRef = useRef(0)
  const activeSourcesRef = useRef<AudioBufferSourceNode[]>([])

  // stopActiveSources cancels every still-scheduled TTS chunk and
  // resets the playback schedule. Called whenever a NEW stream starts
  // while a previous one is still queued (prevents parallel voice
  // overlap) and on explicit stopSpeaking.
  const stopActiveSources = useCallback(() => {
    for (const src of activeSourcesRef.current) {
      try { src.onended = null; src.stop() } catch { /* already stopped */ }
    }
    activeSourcesRef.current = []
    nextTimeRef.current = 0
  }, [])
  const abortRef = useRef<AbortController | null>(null)
  const notifAbortRef = useRef<AbortController | null>(null)
  const micAudioCtxRef = useRef<AudioContext | null>(null)
  const hiddenVideoRef = useRef<HTMLVideoElement | null>(null)
  const notifQueueRef = useRef<{ toolName: string; content: string }[]>([])
  const ignoreIncomingAudioRef = useRef(false)
  // With streaming TTS, sentences are synthesized one at a time so audio
  // chunks arrive with real gaps between them. The "transition back to
  // listening when active sources drain" rule only fires once we've been
  // told no more chunks are coming — otherwise a brief silence between
  // sentences would flip us back to listening while the agent is mid-reply
  // (the talking-head animation is gated on state === 'speaking', so
  // flipping away mid-speech freezes the avatar).
  const audioEndSeenRef = useRef(true)
  const speakingEndedAtRef = useRef(0)
  const lastAiTextRef = useRef('')
  const locationRef = useRef<LocationInfo | null>(null)
  const pendingImagesRef = useRef<Uint8Array[]>([])
  const videoFrameCaptureRef = useRef<(() => Uint8Array | undefined) | null>(null)
  // The two capture sites maintain independent last-sent-hash refs because they are
  // driven by distinct event sources (VAD speech-end vs. manual capture in App.tsx).
  const lastSentHashRef = useRef<bigint | null>(null)
  const IMAGE_SIMILARITY_SKIP_THRESHOLD = 0.80

  // Per-turn reasoning buffer — appended on every ReasoningChunk so the
  // ThinkingPanel updates live instead of waiting for the end-of-turn
  // notification. Reset each time a new turn starts so one turn's
  // reasoning never bleeds into the next.
  const turnReasoningRef = useRef('')

  const transition = useCallback((s: SessionState) => {
    const wasSpeaking = stateRef.current === 'speaking'
    stateRef.current = s
    setState(s)
    // A new inbound turn starts when we begin processing — clear any
    // streamed reasoning from the previous turn so the panel shows only
    // the current turn's thinking as it arrives.
    if (s === 'processing') turnReasoningRef.current = ''
    if (wasSpeaking && s === 'listening') speakingEndedAtRef.current = Date.now()
    // Mute mic during speaking so TTS doesn't leak back into VAD. We do
    // NOT pause/start the VAD worklet here — empirically the library's
    // pause/start cycle doesn't fully revive the worklet within a turn,
    // leaving the mic dead after the first response. setMuted (a
    // user-paced action seconds apart) gets away with it; per-turn
    // transitions don't. Live with the slow VAD drift across many turns
    // and let the user reset via the mute toggle when needed.
    if (mediaStreamRef.current) {
      const track = mediaStreamRef.current.getAudioTracks()[0]
      if (track) {
        if (s === 'speaking') {
          track.enabled = false
        } else if (s === 'listening' && !mutedRef.current) {
          if (wasSpeaking) {
            setTimeout(() => {
              if (stateRef.current === 'listening' && !mutedRef.current) track.enabled = true
            }, 500)
          } else {
            track.enabled = true
          }
        }
      }
    }
    if (vadRef.current) {
      vadRef.current.setOptions({
        positiveSpeechThreshold: s === 'speaking' || mutedRef.current ? 0.99 : 0.5,
      })
    }
  }, [])

  // Stable callbacks surface — mutated via ref so the VAD closure always sees fresh logic
  const callbacksRef = useRef<ConverseCallbacks>(null!)
  callbacksRef.current = {
    onSessionCreated: (id) => {
      sessionIdRef.current = id
      setSessionId(id)
      if (!notifAbortRef.current) {
        notifAbortRef.current = new AbortController()
        subscribeNotifications(id, async (toolName, content) => {
          if (toolName === 'chart') {
            try {
              const spec: ChartSpec = JSON.parse(content)
              setPendingChart(spec)
              emitGesture({ gesture: 'ok', mood: 'happy', moodHoldMs: 2500 })
            } catch (err) {
              console.error('chart payload parse failed:', err, content)
            }
            return
          }
          if (toolName === 'findings') {
            try {
              const spec: FindingsSpec = JSON.parse(content)
              setPendingFindings(spec)
              emitGesture({ gesture: 'index', mood: 'happy', moodHoldMs: 2500 })
            } catch (err) {
              console.error('findings payload parse failed:', err, content)
            }
            return
          }
          if (toolName === 'report') {
            try {
              const spec: ReportSpec = JSON.parse(content)
              setPendingReport(spec)
              emitGesture({ gesture: 'thumbup', mood: 'happy', moodHoldMs: 3500 })
            } catch (err) {
              console.error('report payload parse failed:', err, content)
            }
            return
          }
          if (toolName === 'sensor:spotify_now_playing') {
            try {
              setNowPlaying(JSON.parse(content) as NowPlayingInfo)
              setNowPlayingOpen(true)
            } catch (err) {
              console.error('now-playing payload parse failed:', err, content)
            }
            return
          }
          if (toolName === 'thinking') {
            // Raw reasoning from the conversation LLM — store latest; the
            // UI toggles visibility. Never injected back into the chat.
            setLatestThinking({ at: Date.now(), content })
            return
          }
          if (toolName === 'gesture') {
            try {
              const { gesture, mood } = JSON.parse(content) as { gesture?: GestureName; mood?: MoodName }
              emitGesture({ gesture, mood, moodHoldMs: 3000 })
            } catch (err) {
              console.error('gesture payload parse failed:', err, content)
            }
            return
          }
          if (content.startsWith('Task complete:')) {
            emitGesture({ gesture: 'thumbup', mood: 'happy', moodHoldMs: 3500 })
          } else if (content.startsWith('Task paused:')) {
            emitGesture({ gesture: 'shrug', mood: 'sad', moodHoldMs: 3000 })
          } else if (toolName === 'timer') {
            emitGesture({ gesture: 'handup', mood: 'happy', moodHoldMs: 2500 })
          } else if (toolName === 'task_runner' && content.includes(' calling ')) {
            // per-iteration "calling X" progress ping — a subtle acknowledgement
            emitGesture({ gesture: 'handup' })
          }
          // Audible interruptions — re-inject as a chat turn so the LLM
          // speaks the alert. Timers belong here: the user set them
          // expecting to be told when they fired, not to scan a log panel.
          const isCompletion = content.startsWith('Task complete:') ||
            content.startsWith('Task paused:') ||
            toolName === 'timer'
          if (!isCompletion) {
            setLogs((prev) => [...prev.slice(-49), {
              time: new Date().toLocaleTimeString(),
              tool: toolName, status: 'info', summary: content,
            }])
            return
          }
          if (stateRef.current === 'listening') {
            transition('processing')
            const ac = new AbortController()
            abortRef.current = ac
            await rpcSendText(sessionIdRef.current, `[Notification from ${toolName}]: ${content}`, stableCallbacks, ac.signal, locationRef.current ?? undefined)
            if ((stateRef.current as string) !== 'speaking') transition('listening')
          } else {
            notifQueueRef.current.push({ toolName, content })
          }
        }, notifAbortRef.current.signal)
      }
    },
    onTranscription: (text) => {
      const withinEchoWindow =
        speakingEndedAtRef.current > 0 &&
        Date.now() - speakingEndedAtRef.current < 2000
      if (withinEchoWindow && lastAiTextRef.current && isEcho(text, lastAiTextRef.current)) {
        console.log('Rejected echo transcription:', text)
        ignoreIncomingAudioRef.current = true
        abortRef.current?.abort()
        transition('listening')
      }
    },
    onText: (text) => {
      lastAiTextRef.current = text.toLowerCase()
    },
    onReasoningChunk: (chunk) => {
      // Accumulate and surface live so ThinkingPanel scrolls as the model
      // thinks. The end-of-turn "thinking" notification still fires and
      // overwrites with the final aggregate — identical content, so the
      // transition is invisible.
      turnReasoningRef.current += chunk
      setLatestThinking({ at: Date.now(), content: turnReasoningRef.current })
    },
    onTextChunk: () => {
      // Reserved for a future live-typing reply surface. No-op today:
      // the final aggregate still arrives via onText and drives TTS /
      // lastAiTextRef as before, so streaming is a pure addition.
    },
    onAudioStart: (sampleRate) => {
      if (ignoreIncomingAudioRef.current) return
      // A fresh stream is arriving — stop any still-scheduled chunks
      // from the previous response so we don't play both in parallel.
      // Harmless no-op on a cold start (the array is already empty).
      stopActiveSources()
      if (!audioCtxRef.current || audioCtxRef.current.state === 'closed') {
        audioCtxRef.current = new AudioContext({ sampleRate })
        const an = audioCtxRef.current.createAnalyser()
        an.fftSize = 256
        an.smoothingTimeConstant = 0.75
        ttsAnalyserRef.current = an
        setTtsAnalyser(an)
      }
      if (audioCtxRef.current.state === 'suspended') audioCtxRef.current.resume()
      nextTimeRef.current = audioCtxRef.current.currentTime + 0.05
      // Streaming TTS: more chunks are coming. Block the onended-driven
      // transition-to-listening until AudioEnd signals end-of-speech.
      audioEndSeenRef.current = false
      transition('speaking')
    },
    onAudioChunk: (pcm) => {
      if (ignoreIncomingAudioRef.current) return
      const ctx = audioCtxRef.current
      if (!ctx) return
      const int16 = new Int16Array(pcm.buffer, pcm.byteOffset, pcm.byteLength / 2)
      const float32 = new Float32Array(int16.length)
      for (let i = 0; i < int16.length; i++) float32[i] = int16[i] / 32768
      const buf = ctx.createBuffer(1, float32.length, ctx.sampleRate)
      buf.getChannelData(0).set(float32)
      const source = ctx.createBufferSource()
      source.buffer = buf
      source.connect(ctx.destination)
      if (ttsAnalyserRef.current) source.connect(ttsAnalyserRef.current)
      const startAt = Math.max(nextTimeRef.current, ctx.currentTime)
      source.start(startAt)
      nextTimeRef.current = startAt + buf.duration
      activeSourcesRef.current.push(source)
      source.onended = () => {
        // Drop this source from the active array. Other chunks from the
        // same stream may still be queued, AND with streaming TTS more
        // chunks may not have arrived yet — only transition back to
        // listening once the array drains AND we've been told no more
        // audio is coming (AudioEnd).
        const arr = activeSourcesRef.current
        const idx = arr.indexOf(source)
        if (idx >= 0) arr.splice(idx, 1)
        if (arr.length === 0 && audioEndSeenRef.current && stateRef.current === 'speaking') {
          transition('listening')
        }
      }
    },
    onAudioEnd: () => {
      // Unblock the onended-driven transition: future source.onended
      // callbacks are now allowed to flip us back to listening. If the
      // active source array is already empty (the last chunk finished
      // playing before the end signal arrived — common on short
      // replies), transition now.
      audioEndSeenRef.current = true
      if (ignoreIncomingAudioRef.current) {
        ignoreIncomingAudioRef.current = false
        abortRef.current?.abort()
        transition('listening')
        return
      }
      if (activeSourcesRef.current.length === 0 && stateRef.current === 'speaking') transition('listening')
    },
    onError: (err) => {
      console.error('converse error:', err)
      transition('listening')
    },
    onToolStatus: (toolName, status, summary) => {
      setLogs(prev => [...prev.slice(-49), {
        time: new Date().toLocaleTimeString(), tool: toolName, status, summary,
      }])
    },
  }

  // `stableCallbacks` is a thin forwarding shell around `callbacksRef.current`.
  // Its object identity changes every render but that doesn't matter: the VAD
  // closure captures it once at init and always dereferences through the ref
  // to the latest logic. Do not try to memoize this — the indirection is what
  // makes stale closures impossible.
  const stableCallbacks: ConverseCallbacks = {
    onSessionCreated: (id) => callbacksRef.current.onSessionCreated(id),
    onTranscription: (t, d) => callbacksRef.current.onTranscription?.(t, d),
    onText: (t, d) => callbacksRef.current.onText(t, d),
    onTextChunk: (t) => callbacksRef.current.onTextChunk?.(t),
    onReasoningChunk: (t) => callbacksRef.current.onReasoningChunk?.(t),
    onAudioStart: (sr, sc) => callbacksRef.current.onAudioStart?.(sr, sc),
    onAudioChunk: (p, i) => callbacksRef.current.onAudioChunk?.(p, i),
    onAudioEnd: (d) => callbacksRef.current.onAudioEnd?.(d),
    onError: (e) => callbacksRef.current.onError?.(e),
    onToolStatus: (n, s, sm) => callbacksRef.current.onToolStatus?.(n, s, sm),
  }

  const initedRef = useRef(false)
  useEffect(() => {
    if (initedRef.current) return
    initedRef.current = true

    let vad: any = null
    let stream: MediaStream | null = null

    async function init() {
      try {
        stream = await navigator.mediaDevices.getUserMedia({
          video: { width: 640, height: 480, facingMode: 'user' },
          audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
        })
      } catch (e) {
        console.warn('combined getUserMedia failed, trying separately:', e)
        const results = await Promise.allSettled([
          navigator.mediaDevices.getUserMedia({ video: { width: 640, height: 480, facingMode: 'user' } }),
          navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } }),
        ])
        stream = new MediaStream()
        for (const r of results) {
          if (r.status === 'fulfilled') r.value.getTracks().forEach(t => stream!.addTrack(t))
        }
      }

      mediaStreamRef.current = stream
      setMediaStream(stream)

      // Hidden off-screen video element so we can capture frames silently
      const hiddenVideo = document.createElement('video')
      hiddenVideo.autoplay = true
      hiddenVideo.playsInline = true
      hiddenVideo.muted = true
      hiddenVideo.style.position = 'absolute'
      hiddenVideo.style.left = '-9999px'
      hiddenVideo.srcObject = stream
      document.body.appendChild(hiddenVideo)
      hiddenVideoRef.current = hiddenVideo

      videoFrameCaptureRef.current = () => {
        if (!hiddenVideo.videoWidth || hiddenVideo.readyState < 2) return undefined
        const canvas = document.createElement('canvas')
        const scale = 320 / hiddenVideo.videoWidth
        canvas.width = 320
        canvas.height = Math.round(hiddenVideo.videoHeight * scale)
        canvas.getContext('2d')!.drawImage(hiddenVideo, 0, 0, canvas.width, canvas.height)
        const hash = dHash(canvas)
        const prev = lastSentHashRef.current
        if (prev !== null && hammingSimilarity(prev, hash) >= IMAGE_SIMILARITY_SKIP_THRESHOLD) {
          return undefined
        }
        lastSentHashRef.current = hash
        const dataUrl = canvas.toDataURL('image/jpeg', 0.7)
        const bin = atob(dataUrl.split(',')[1])
        const bytes = new Uint8Array(bin.length)
        for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
        return bytes
      }

      // Mic analyser
      if (stream && stream.getAudioTracks().length > 0) {
        const ctx = new AudioContext()
        micAudioCtxRef.current = ctx
        if (ctx.state === 'suspended') await ctx.resume()
        const source = ctx.createMediaStreamSource(new MediaStream(stream.getAudioTracks()))
        const analyser = ctx.createAnalyser()
        analyser.fftSize = 256
        analyser.smoothingTimeConstant = 0.75
        source.connect(analyser)
        const silentGain = ctx.createGain()
        silentGain.gain.value = 0
        analyser.connect(silentGain)
        silentGain.connect(ctx.destination)
        setMicAnalyser(analyser)
      }

      // VAD
      const vadGlobal = (window as any).vad
      if (!vadGlobal) throw new Error('VAD global not loaded')
      vad = await vadGlobal.MicVAD.new({
        processorType: 'AudioWorklet',
        getStream: async () => new MediaStream(stream!.getAudioTracks()),
        positiveSpeechThreshold: 0.5,
        negativeSpeechThreshold: 0.25,
        redemptionMs: 600,
        minSpeechMs: 300,
        preSpeechPadMs: 300,
        onSpeechStart: () => {},
        onSpeechEnd: async (audio: Float32Array) => {
          if (stateRef.current !== 'listening') return
          if (audio.length < 1600) return
          if (Date.now() - speakingEndedAtRef.current < 750) return
          transition('processing')
          const wav = float32ToWav(audio, 16000)
          const imageJpeg = pendingImagesRef.current.length > 0
            ? pendingImagesRef.current[0]
            : videoFrameCaptureRef.current?.()
          if (pendingImagesRef.current.length > 0) pendingImagesRef.current = []
          abortRef.current = new AbortController()
          await rpcSendAudio(sessionIdRef.current, wav, imageJpeg, stableCallbacks, abortRef.current.signal, locationRef.current ?? undefined)
          if ((stateRef.current as string) !== 'speaking') transition('listening')
        },
        onVADMisfire: () => {},
        onnxWASMBasePath: '/onnx/',
        baseAssetPath: '/vad/',
      })
      vadRef.current = vad
      vad.start()
      transition('listening')

      if (navigator.geolocation) {
        navigator.geolocation.getCurrentPosition(
          (pos) => { locationRef.current = { latitude: pos.coords.latitude, longitude: pos.coords.longitude } },
          () => {},
          { enableHighAccuracy: false, timeout: 10000 },
        )
      }
    }

    init().catch((err) => {
      console.error('presence init failed:', err)
      transition('listening')
    })

    return () => {
      vad?.destroy()
      stream?.getTracks().forEach((t: MediaStreamTrack) => t.stop())
      if (hiddenVideoRef.current) {
        hiddenVideoRef.current.srcObject = null
        hiddenVideoRef.current.remove()
        hiddenVideoRef.current = null
      }
      micAudioCtxRef.current?.close().catch(() => {})
      micAudioCtxRef.current = null
      notifAbortRef.current?.abort()
      notifAbortRef.current = null
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const send = useCallback(async (text: string, images?: Uint8Array[]) => {
    const id = sessionIdRef.current
    if (!text.trim() || !id) return
    if (stateRef.current === 'processing' || stateRef.current === 'speaking') return
    if (images && images.length) pendingImagesRef.current = images
    transition('processing')
    abortRef.current = new AbortController()
    await rpcSendText(id, text, stableCallbacks, abortRef.current.signal, locationRef.current ?? undefined)
    if ((stateRef.current as string) !== 'speaking') transition('listening')
  }, [transition])

  const stopSpeaking = useCallback(() => {
    ignoreIncomingAudioRef.current = true
    abortRef.current?.abort()
    stopActiveSources()
    if (audioCtxRef.current && audioCtxRef.current.state !== 'closed') {
      audioCtxRef.current.close().catch(() => {})
      audioCtxRef.current = null
      ttsAnalyserRef.current = null
      setTtsAnalyser(null)
    }
    transition('listening')
  }, [transition, stopActiveSources])

  const attachImage = useCallback((jpeg: Uint8Array) => {
    pendingImagesRef.current = [...pendingImagesRef.current, jpeg]
  }, [])

  // Poll pending proposals on each return to listening (lightweight — fast RPCs)
  useEffect(() => {
    if (state !== 'listening') return
    let cancelled = false
    ;(async () => {
      try {
        const [tools, prompts] = await Promise.all([
          adminClient.listToolProposals(create(ListToolProposalsRequestSchema, {})),
          adminClient.listPromptProposals(create(ListPromptProposalsRequestSchema, {})),
        ])
        if (cancelled) return
        const pending = tools.proposals.filter(p => p.status === 'pending').length +
                        prompts.proposals.filter(p => p.status === 'pending').length
        setPendingProposals(pending)
      } catch (err) {
        console.error('proposal poll failed:', err)
      }
    })()
    return () => { cancelled = true }
  }, [state])

  // Drain queued task completions when we return to listening
  useEffect(() => {
    if (state !== 'listening') return
    if (notifQueueRef.current.length === 0) return
    const n = notifQueueRef.current.shift()!
    const id = sessionIdRef.current
    if (!id) return
    transition('processing')
    const ac = new AbortController()
    abortRef.current = ac
    rpcSendText(id, `[Notification from ${n.toolName}]: ${n.content}`, stableCallbacks, ac.signal, locationRef.current ?? undefined)
      .then(() => { if (stateRef.current !== 'speaking') transition('listening') })
    return () => ac.abort()
  }, [state, transition])

  const setMuted = useCallback((m: boolean) => {
    mutedRef.current = m
    setMutedState(m)
    if (mediaStreamRef.current) {
      mediaStreamRef.current.getAudioTracks().forEach(t => { t.enabled = !m })
    }
    if (vadRef.current) {
      vadRef.current.setOptions({ positiveSpeechThreshold: m ? 0.99 : 0.5 })
      // Toggling track.enabled alone leaves the VAD worklet running on
      // silence — for long mutes the internal state drifts (the model
      // accumulates silent frames) and it stops triggering once the
      // track resumes. Using the library's own pause/start resets the
      // processor cleanly on each transition.
      if (m) vadRef.current.pause()
      else vadRef.current.start()
    }
    // Resume the audio context if the browser auto-suspended it during
    // the quiet period. Without this, the VAD's analyser source stays
    // frozen even after vad.start() re-enables the worklet.
    if (!m && micAudioCtxRef.current?.state === 'suspended') {
      micAudioCtxRef.current.resume().catch(() => {})
    }
  }, [])

  const dismissChart = useCallback(() => setPendingChart(null), [])
  const dismissNowPlaying = useCallback(() => setNowPlayingOpen(false), [])
  const dismissFindings = useCallback(() => setPendingFindings(null), [])
  const dismissReport = useCallback(() => setPendingReport(null), [])

  // After each turn completes (speaking → listening), refresh the
  // persisted tool-call detail for the current session. The panel only
  // needs this to be fresh at turn boundaries; continuous polling would
  // be wasteful and the ToolStatus stream already handles the in-flight
  // "called X, doing Y" display via `logs`.
  useEffect(() => {
    if (state !== 'listening' || !sessionId) return
    let cancelled = false
    ;(async () => {
      try {
        const resp = await adminClient.listToolCalls(create(ListToolCallsRequestSchema, { limit: 100, offset: 0 }))
        if (cancelled) return
        const rows: ToolCallEntry[] = resp.calls
          .filter(c => c.sessionId === sessionId)
          .map(c => ({
            at: Date.parse(c.createdAt) || Date.now(),
            name: c.toolName,
            provider: c.provider,
            duration_ms: c.durationMs,
            args: c.args || undefined,
            result: c.result || undefined,
            error: c.error || undefined,
          }))
          .sort((a, b) => a.at - b.at)
        setToolCalls(rows)
      } catch (err) {
        console.warn('listToolCalls refresh failed:', err)
      }
    })()
    return () => { cancelled = true }
  }, [state, sessionId])

  // Idle animator — keeps the avatar feeling alive during long silences.
  // While the user has been in 'listening' with no recent LLM-driven
  // gesture, fire a subtle ambient cue every 25–45s so the avatar
  // doesn't feel frozen. Heavier, expressive gestures belong to the
  // LLM's `gesture` tool; these are deliberately small-mood / subtle-
  // hand cues so they never compete with a real reply.
  useEffect(() => {
    if (state !== 'listening') return
    // Idle pool — mood-only nudges plus two small gestures. Each entry
    // carries a short moodHoldMs so the mood reverts quickly and
    // state-derived mood resumes.
    const pool: Omit<GestureCue, 'ts'>[] = [
      { mood: 'happy', moodHoldMs: 1800 },
      { mood: 'love', moodHoldMs: 2000 },
      { mood: 'neutral', moodHoldMs: 1200 },
      { gesture: 'ok', mood: 'neutral', moodHoldMs: 1500 },
      { gesture: 'side', mood: 'neutral', moodHoldMs: 1500 },
      { gesture: 'shrug', moodHoldMs: 1600 },
    ]
    // Wait at least this long after entering listening before the first
    // idle cue — avoids firing one right as the user's turn ends.
    const FIRST_IDLE_MIN_MS = 15_000
    // Minimum gap since the last real or idle cue before firing another.
    const MIN_QUIET_MS = 20_000
    const listeningStartedAt = Date.now()
    let timer: ReturnType<typeof setTimeout> | null = null
    const scheduleNext = () => {
      const delay = 25_000 + Math.random() * 20_000
      timer = setTimeout(tick, delay)
    }
    const tick = () => {
      timer = null
      if (stateRef.current !== 'listening') return
      const now = Date.now()
      if (now - listeningStartedAt < FIRST_IDLE_MIN_MS) {
        scheduleNext()
        return
      }
      const recentCue = gestureCue?.ts ?? 0
      if (now - recentCue < MIN_QUIET_MS) {
        scheduleNext()
        return
      }
      const pick = pool[Math.floor(Math.random() * pool.length)]
      emitGesture(pick)
      scheduleNext()
    }
    scheduleNext()
    return () => { if (timer) clearTimeout(timer) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state])

  return {
    state, ttsAnalyser, micAnalyser, sessionId,
    send, stopSpeaking, muted, setMuted,
    pendingChart, dismissChart,
    pendingFindings, dismissFindings,
    pendingReport, dismissReport,
    latestThinking,
    gestureCue,
    pendingProposals, logs, toolCalls, attachImage,
    mediaStream,
    nowPlaying,
    nowPlayingOpen,
    dismissNowPlaying,
  }
}

function isEcho(transcription: string, lastAiText: string): boolean {
  const tWords = new Set(
    transcription.toLowerCase().replace(/[^\w\s]/g, '').split(/\s+/).filter(w => w.length > 2),
  )
  const aWords = new Set(
    lastAiText.replace(/[^\w\s]/g, '').split(/\s+/).filter(w => w.length > 2),
  )
  if (tWords.size === 0) return false
  let overlap = 0
  for (const w of tWords) if (aWords.has(w)) overlap++
  return overlap / tWords.size > 0.5
}

function float32ToWav(samples: Float32Array, sampleRate: number): Uint8Array {
  const buf = new ArrayBuffer(44 + samples.length * 2)
  const view = new DataView(buf)
  const w = (off: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(off + i, s.charCodeAt(i))
  }
  w(0, 'RIFF')
  view.setUint32(4, 36 + samples.length * 2, true)
  w(8, 'WAVE'); w(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true); view.setUint16(22, 1, true)
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * 2, true)
  view.setUint16(32, 2, true); view.setUint16(34, 16, true)
  w(36, 'data'); view.setUint32(40, samples.length * 2, true)
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]))
    view.setInt16(44 + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true)
  }
  return new Uint8Array(buf)
}

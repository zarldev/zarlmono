import { useRef, useEffect } from 'react'
import { TalkingHead } from '@met4citizen/talkinghead'
import type { SessionState, GestureCue } from './hooks/usePresenceSession'
import type { Avatar } from './avatars'
import { DEFAULT_AVATAR } from './avatars'
import { registerCustomGestures } from './talkingHeadGestures'

// LiveTemplate is a one-shot override for a named gesture template.
// When the object reference changes (new `ts`), the component replaces
// head.gestureTemplates[name] with `template` and immediately plays it.
// Used by the admin gesture playground's slider-tuner so rotation edits
// preview without a rebuild.
export interface LiveTemplate {
  ts: number
  name: string
  template: Record<string, unknown>
}

interface Props {
  analyser: AnalyserNode | null
  state: SessionState
  avatar?: Avatar
  gestureCue?: GestureCue | null
  // interactive enables click-and-drag rotation of the camera around the head
  // (zoom/pan stay off — users only get to spin). Defaults to false because
  // the immersive conversation screen doesn't want stray drags during talk.
  interactive?: boolean
  // liveTemplate swaps a gesture template in-place and plays it — the
  // admin playground feeds slider values through this. Safe to leave
  // unset; nil has no effect on the normal gesture-cue pipeline.
  liveTemplate?: LiveTemplate | null
}

function stateMood(state: SessionState): string {
  switch (state) {
    case 'speaking':    return 'happy'
    case 'processing':  return 'neutral'
    case 'listening':   return 'neutral'
    default:            return 'neutral'
  }
}

// Oculus viseme set — matches the blend shapes present on Avaturn/RPM meshes.
// Indices are used in the frequency-band mapping below; keep them in sync.
const VISEMES = [
  'viseme_sil', // 0
  'viseme_PP',  // 1
  'viseme_FF',  // 2
  'viseme_TH',  // 3
  'viseme_DD',  // 4
  'viseme_kk',  // 5
  'viseme_CH',  // 6
  'viseme_SS',  // 7
  'viseme_nn',  // 8
  'viseme_RR',  // 9
  'viseme_aa',  // 10
  'viseme_E',   // 11
  'viseme_I',   // 12
  'viseme_O',   // 13
  'viseme_U',   // 14
] as const

export default function TalkingHeadLib({ analyser, state, avatar, gestureCue, liveTemplate, interactive = false }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const headRef = useRef<TalkingHead | null>(null)
  const readyRef = useRef(false)
  const analyserRef = useRef(analyser)
  const stateRef = useRef(state)
  analyserRef.current = analyser
  stateRef.current = state
  const activeAvatar = avatar ?? DEFAULT_AVATAR
  const avatarRef = useRef(activeAvatar)
  avatarRef.current = activeAvatar

  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const head = new TalkingHead(el, {
      modelPixelRatio: Math.min(window.devicePixelRatio, 2),
      cameraView: 'full',
      // Pull the camera back and shift the look-target up so the head sits
      // near the vertical middle of the frame instead of low-down. Positive
      // cameraY raises the look-target; cameraDistance is additive on top of
      // the preset.
      cameraDistance: 0.4,
      cameraY: 0.25,
      cameraX: 0,
      // Disable built-in TTS; we drive audio ourselves
      ttsEndpoint: null,
      // No lipsync modules — we drive visemes via mtAvatar.realtime injection
      lipsyncModules: [],
      lightAmbientColor: 0xffffff,
      lightAmbientIntensity: 2,
      lightDirectColor: 0x8888aa,
      lightDirectIntensity: 30,
      cameraRotateEnable: interactive,
      cameraPanEnable: false,
      cameraZoomEnable: false,
    })
    headRef.current = head

    // Smoothed viseme weights — lerped each frame so the mouth relaxes naturally
    // when speech stops.
    const visemeWeights = new Float32Array(VISEMES.length)

    let raf = 0

    // Settle-loop: load whatever avatarRef points at; if the prop changed
    // while showAvatar was in flight (e.g. a query hydrated a different saved
    // avatar after the default-mount), loop and load again. This closes the
    // race that otherwise leaves the head stuck on whichever GLB the first
    // render happened to pick.
    ;(async () => {
      let lastLoadedUrl = ''
      while (lastLoadedUrl !== avatarRef.current.url) {
        const url = avatarRef.current.url
        const body = avatarRef.current.body
        readyRef.current = false
        try {
          await head.showAvatar({ url, body, avatarMood: 'neutral' })
        } catch (err) {
          console.error('TalkingHead avatar load failed:', err)
          return
        }
        lastLoadedUrl = url
      }
      registerCustomGestures(head)
      readyRef.current = true
      head.setMood(stateMood(stateRef.current))
    })()

    // Inject viseme weights into the library's morph target pipeline via
    // mtAvatar[name].realtime. When realtime !== null the library's animate()
    // uses that value directly, bypassing its own keyframe/tween logic — so
    // our values are applied before render() is called. No RAF order fighting.
    function injectVisemes() {
      const mt = head.mtAvatar
      if (!mt) return
      for (let i = 0; i < VISEMES.length; i++) {
        const entry = mt[VISEMES[i]]
        if (!entry) continue
        const w = visemeWeights[i]
        // Set realtime to null when weight is negligible so the library can
        // resume baseline control (keeps blink/expression overrides intact).
        entry.realtime = w > 0.001 ? w : null
        // CRUCIAL: without needsUpdate the library's animate() skips this morph
        // (see talkinghead.mjs line 1591: `if (!o.needsUpdate) continue`).
        entry.needsUpdate = true
      }
    }

    function clearRealtimeVisemes() {
      const mt = head.mtAvatar
      if (!mt) return
      for (const name of VISEMES) {
        const entry = mt[name]
        if (entry) {
          entry.realtime = null
          entry.needsUpdate = true
        }
      }
    }

    function tick() {
      raf = requestAnimationFrame(tick)

      const an = analyserRef.current
      const s = stateRef.current
      const target = new Float32Array(VISEMES.length)

      if (an && s === 'speaking') {
        const freqData = new Float32Array(an.frequencyBinCount)
        an.getFloatFrequencyData(freqData)
        const binCount = freqData.length
        const band1End = Math.floor(binCount * 0.08)
        const band2End = Math.floor(binCount * 0.25)
        const band3End = Math.floor(binCount * 0.5)
        let low = 0, mid = 0, high = 0, total = 0
        for (let i = 1; i < band3End; i++) {
          const e = Math.pow(10, (freqData[i] + 100) / 40)
          if (i < band1End) low += e
          else if (i < band2End) mid += e
          else high += e
          total += e
        }
        const timeBuf = new Float32Array(an.fftSize)
        an.getFloatTimeDomainData(timeBuf)
        let rmsSum = 0
        for (let j = 0; j < timeBuf.length; j++) rmsSum += timeBuf[j] * timeBuf[j]
        const amplitude = Math.min(Math.sqrt(rmsSum / timeBuf.length) * 6, 1.0)
        if (amplitude > 0.02) {
          const norm = total + 0.001
          const lowR = low / norm, midR = mid / norm, highR = high / norm
          target[10] = lowR * 0.8 * amplitude          // viseme_aa
          target[13] = lowR * 0.4 * amplitude          // viseme_O
          target[11] = midR * 0.6 * amplitude          // viseme_E
          target[12] = midR * 0.4 * amplitude          // viseme_I
          target[4]  = midR * 0.3 * amplitude          // viseme_DD
          target[8]  = midR * 0.3 * amplitude          // viseme_nn
          target[7]  = highR * 0.6 * amplitude         // viseme_SS
          target[2]  = highR * 0.3 * amplitude         // viseme_FF
          target[3]  = highR * 0.2 * amplitude         // viseme_TH
          target[6]  = highR * 0.3 * amplitude         // viseme_CH
          target[1]  = Math.max(0, amplitude - 0.5) * midR * 0.5  // viseme_PP
          target[5]  = Math.max(0, amplitude - 0.4) * midR * 0.3  // viseme_kk
          target[14] = lowR * midR * amplitude * 0.5   // viseme_U
          target[9]  = midR * lowR * amplitude * 0.4   // viseme_RR
        }
      }

      // Smooth crossfade toward target (all zeros when not speaking)
      let anyNonZero = false
      for (let i = 0; i < VISEMES.length; i++) {
        visemeWeights[i] += (target[i] - visemeWeights[i]) * 0.35
        if (visemeWeights[i] > 0.001) anyNonZero = true
      }

      if (anyNonZero) {
        injectVisemes()
      } else {
        // All weights negligible — clear realtime so library resumes baseline
        clearRealtimeVisemes()
      }
    }
    tick()

    return () => {
      cancelAnimationFrame(raf)
      clearRealtimeVisemes()
      head.stop()
      readyRef.current = false
      headRef.current = null
    }
  }, [])

  useEffect(() => {
    if (!readyRef.current) return
    headRef.current?.setMood(stateMood(state))
  }, [state])

  // React to gesture/mood cues pushed from the hook. Each cue carries a fresh
  // `ts`, so identical gestures back-to-back still fire (the effect runs on
  // object-identity change). When a mood is set we schedule a revert to the
  // state-derived mood so we don't get stuck on "happy" forever.
  useEffect(() => {
    if (!gestureCue) return
    const head = headRef.current
    if (!head || !readyRef.current) return
    if (gestureCue.gesture) {
      try { head.playGesture(gestureCue.gesture, 3) } catch (err) {
        console.warn('playGesture failed:', err)
      }
    }
    if (gestureCue.mood) {
      head.setMood(gestureCue.mood)
      const hold = gestureCue.moodHoldMs ?? 3000
      const t = setTimeout(() => {
        if (headRef.current && readyRef.current) {
          headRef.current.setMood(stateMood(stateRef.current))
        }
      }, hold)
      return () => clearTimeout(t)
    }
  }, [gestureCue])

  // Slider-tuner override: replace the named gesture template with the
  // provided rotation map and play it. Skipped when unset (production
  // rendering path).
  useEffect(() => {
    if (!liveTemplate) return
    const head = headRef.current
    if (!head || !readyRef.current) return
    head.gestureTemplates[liveTemplate.name] = liveTemplate.template
    try { head.playGesture(liveTemplate.name, 2) } catch (err) {
      console.warn('live playGesture failed:', err)
    }
  }, [liveTemplate])

  // Hot-swap avatar when the user picks a different one. We intentionally only
  // react to URL changes (body is carried along); the initial mount is handled
  // by the big useEffect above.
  useEffect(() => {
    const head = headRef.current
    if (!head || !readyRef.current) return
    readyRef.current = false
    head.showAvatar({
      url: activeAvatar.url,
      body: activeAvatar.body,
      avatarMood: 'neutral',
    }).then(() => {
      registerCustomGestures(head)
      readyRef.current = true
      head.setMood(stateMood(stateRef.current))
    }).catch((err: unknown) => {
      console.error('TalkingHead avatar swap failed:', err)
    })
  }, [activeAvatar.url, activeAvatar.body])

  return (
    <div
      ref={containerRef}
      className="absolute inset-0 z-10"
      style={{ width: '100%', height: '100%' }}
    />
  )
}

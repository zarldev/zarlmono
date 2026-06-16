import { useEffect, useRef } from 'react'
import type { SessionState } from '@/hooks/usePresenceSession'

interface Props {
  state: SessionState
  ttsAnalyser: AnalyserNode | null
  micAnalyser: AnalyserNode | null
}

const STATE_COLOR: Record<SessionState, string> = {
  loading:    'rgba(200, 202, 208, 0.6)',
  listening:  '#93c5fd',
  processing: '#a78bfa',
  speaking:   '#f59e0b',
}

const STATE_LABEL: Record<SessionState, string> = {
  loading:    'loading',
  listening:  'listening',
  processing: 'thinking',
  speaking:   'speaking',
}

// Five frequency bands we sample from the analyser, spaced
// logarithmically across the spectrum so the bars look lively rather
// than all pulsing in lockstep. Numbers are byte-count offsets into the
// fftSize=256 buffer (0..127).
const BAND_RANGES: Array<[number, number]> = [
  [2, 6],      // low
  [6, 14],     // low-mid
  [14, 30],    // mid
  [30, 60],    // high-mid
  [60, 110],   // high
]

// Idle height when the analyser is attached but quiet — keeps the bars
// visible at a calm baseline instead of dropping to zero.
const IDLE = 0.12

function bandEnergy(data: Uint8Array, start: number, end: number): number {
  let sum = 0
  const n = Math.max(1, end - start)
  for (let i = start; i < end && i < data.length; i++) sum += data[i]
  return (sum / n) / 255
}

// StateMonitor is a compact operator readout showing the current
// session state with a visualiser that actually reflects what's
// happening: live mic VU when listening, TTS VU when speaking, a
// scanning line when thinking, and a staggered pulse when loading.
export default function StateMonitor({ state, ttsAnalyser, micAnalyser }: Props) {
  const barsRef = useRef<Array<HTMLSpanElement | null>>([null, null, null, null, null])
  const rafRef = useRef<number>(0)
  const stateRef = useRef(state)
  stateRef.current = state
  const micRef = useRef(micAnalyser)
  micRef.current = micAnalyser
  const ttsRef = useRef(ttsAnalyser)
  ttsRef.current = ttsAnalyser

  useEffect(() => {
    const buf = new Uint8Array(128)

    function tick() {
      rafRef.current = requestAnimationFrame(tick)
      const s = stateRef.current
      const analyser =
        s === 'listening' ? micRef.current :
        s === 'speaking'  ? ttsRef.current :
        null

      if (!analyser) {
        // Non-audio-reactive states (loading / processing) — keep the
        // bars at a calm idle; the state-specific CSS animation carries
        // the visual energy instead.
        for (const bar of barsRef.current) {
          if (bar) bar.style.transform = `scaleY(${IDLE})`
        }
        return
      }

      analyser.getByteFrequencyData(buf)
      for (let i = 0; i < BAND_RANGES.length; i++) {
        const [lo, hi] = BAND_RANGES[i]
        const raw = bandEnergy(buf, lo, hi)
        // Compress dynamic range so quiet speech still lifts the bars.
        const eased = Math.pow(raw, 0.6)
        const scale = Math.max(IDLE, eased)
        const bar = barsRef.current[i]
        if (bar) bar.style.transform = `scaleY(${scale})`
      }
    }

    rafRef.current = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(rafRef.current)
  }, [])

  const color = STATE_COLOR[state]
  const label = STATE_LABEL[state]
  const showBars = state === 'listening' || state === 'speaking'
  const showScan = state === 'processing'
  const showDots = state === 'loading'

  return (
    <div className="flex items-center gap-2.5 op-mono text-[10px]">
      <span style={{ color }} className="transition-colors duration-300">{label}</span>

      <span
        className="relative block h-3 w-12 flex items-end justify-center gap-[3px] overflow-hidden"
        aria-hidden
      >
        {showBars && [0, 1, 2, 3, 4].map((i) => (
          <span
            key={i}
            ref={(el) => { barsRef.current[i] = el }}
            className="block w-[2px] h-full origin-bottom transition-transform duration-75"
            style={{ background: color, transform: `scaleY(${IDLE})` }}
          />
        ))}

        {showScan && (
          <span
            className="absolute top-1/2 -translate-y-1/2 h-[1px] w-6 opacity-80"
            style={{ background: color, animation: 'op-scan 1.4s cubic-bezier(0.6,0,0.4,1) infinite' }}
          />
        )}

        {showDots && (
          <span className="flex items-center justify-center gap-1 h-full w-full">
            {[0, 1, 2].map((i) => (
              <span
                key={i}
                className="block w-1 h-1 rounded-full"
                style={{
                  background: color,
                  animation: 'op-dot-pulse 1.3s ease-in-out infinite',
                  animationDelay: `${i * 180}ms`,
                }}
              />
            ))}
          </span>
        )}
      </span>
    </div>
  )
}

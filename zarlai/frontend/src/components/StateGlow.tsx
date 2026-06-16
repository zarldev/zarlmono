import { useEffect, useRef } from 'react'
import type { SessionState } from '@/hooks/usePresenceSession'

const STATE_COLORS: Record<SessionState, [string, string]> = {
  loading:    ['rgba(58,61,70,0.12)',    'rgba(58,61,70,0.04)'],
  listening:  ['rgba(147,197,253,0.18)', 'rgba(147,197,253,0.05)'],
  processing: ['rgba(167,139,250,0.22)', 'rgba(167,139,250,0.06)'],
  speaking:   ['rgba(245,158,11,0.25)',  'rgba(245,158,11,0.07)'],
}

interface Props {
  state: SessionState
  analyser: AnalyserNode | null
}

export default function StateGlow({ state, analyser }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  const analyserRef = useRef(analyser)
  analyserRef.current = analyser
  const stateRef = useRef(state)
  stateRef.current = state

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const [main, dim] = STATE_COLORS[state]
    el.style.setProperty('--glow-main', main)
    el.style.setProperty('--glow-dim', dim)
  }, [state])

  useEffect(() => {
    const el = ref.current
    if (!el) return
    let raf = 0
    function tick() {
      raf = requestAnimationFrame(tick)
      const an = analyserRef.current
      if (!an || stateRef.current !== 'speaking') {
        el!.style.filter = 'brightness(1)'
        return
      }
      const buf = new Float32Array(an.fftSize)
      an.getFloatTimeDomainData(buf)
      let sum = 0
      for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i]
      const rms = Math.sqrt(sum / buf.length)
      const brightness = 1 + Math.min(0.4, rms * 4)
      el!.style.filter = `brightness(${brightness.toFixed(3)})`
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [])

  return (
    <div
      ref={ref}
      className="fixed inset-0 pointer-events-none z-0 transition-[background] duration-[400ms]"
      style={{
        background: `
          radial-gradient(ellipse 40% 30% at 50% 90%, var(--glow-main), transparent 70%),
          radial-gradient(ellipse 60% 40% at 50% 10%, var(--glow-dim), transparent 70%),
          radial-gradient(ellipse 30% 40% at 10% 50%, var(--glow-dim), transparent 70%),
          radial-gradient(ellipse 30% 40% at 90% 50%, var(--glow-dim), transparent 70%)
        `,
      }}
    />
  )
}

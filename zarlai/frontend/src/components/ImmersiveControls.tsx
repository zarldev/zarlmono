// frontend/src/components/ImmersiveControls.tsx
import type { SessionState } from '@/hooks/usePresenceSession'

const STATE_LABEL: Record<SessionState, string> = {
  loading: 'Loading', listening: 'Listening', processing: 'Thinking', speaking: 'Speaking',
}
const STATE_DOT: Record<SessionState, string> = {
  loading:    '#3a3d46',
  listening:  '#93c5fd',
  processing: '#a78bfa',
  speaking:   '#f59e0b',
}

interface Props {
  state: SessionState
  muted: boolean
  onToggleMute: () => void
  onOpenAdmin: () => void
  pendingProposals: number
  visible: boolean
}

export default function ImmersiveControls({ state, muted, onToggleMute, onOpenAdmin, pendingProposals, visible }: Props) {
  const op = visible ? 'opacity-100' : 'opacity-35'
  return (
    <>
      <button
        onClick={onToggleMute}
        title={muted ? 'Unmute (M)' : 'Mute (M)'}
        className={`fixed top-4 left-4 z-30 w-5 h-5 rounded transition-opacity duration-300 ${op} ${visible ? 'hover:opacity-100' : 'pointer-events-none'}`}
        style={{ color: muted ? '#f87171' : 'rgba(255,255,255,0.6)' }}
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          {muted ? (
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
      </button>

      <div className={`fixed top-4 right-4 z-30 flex items-center gap-2 transition-opacity duration-300 ${op} ${visible ? '' : 'pointer-events-none'}`}>
        <div className="flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white/[0.04] text-[9px] uppercase tracking-widest text-white/60">
          <span className="w-1.5 h-1.5 rounded-full" style={{ background: STATE_DOT[state] }} />
          {STATE_LABEL[state]}
        </div>
        <button
          onClick={onOpenAdmin}
          title="Admin (Cmd+K)"
          className="relative w-5 h-5 rounded hover:opacity-100"
          style={{ color: 'rgba(255,255,255,0.6)' }}
        >
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/>
            <circle cx="12" cy="12" r="3"/>
          </svg>
          {pendingProposals > 0 && (
            <span className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-[#f59e0b] border border-[#07090f]" />
          )}
        </button>
      </div>
    </>
  )
}

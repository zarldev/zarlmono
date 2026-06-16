import { useState, useRef, useEffect } from 'react'
import { AVATARS, type Avatar } from '@/avatars'

interface Props {
  current: Avatar
  onSelect: (a: Avatar) => void
  visible: boolean
}

export default function AvatarPicker({ current, onSelect, visible }: Props) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    function onClick(e: MouseEvent) {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false)
    }
    window.addEventListener('mousedown', onClick)
    return () => window.removeEventListener('mousedown', onClick)
  }, [open])

  const op = visible ? 'opacity-100' : 'opacity-35'

  return (
    <div
      ref={rootRef}
      className={`fixed top-4 left-12 z-30 transition-opacity duration-300 ${op} ${visible || open ? '' : 'pointer-events-none'}`}
    >
      <button
        onClick={() => setOpen((o) => !o)}
        title="Avatar"
        className="w-5 h-5 rounded hover:opacity-100"
        style={{ color: 'rgba(255,255,255,0.6)' }}
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
          <circle cx="12" cy="7" r="4" />
        </svg>
      </button>

      {open && (
        <div className="absolute top-7 left-0 min-w-[140px] rounded-lg border border-white/10 bg-[#0f1218]/90 backdrop-blur-md shadow-2xl py-1">
          {AVATARS.map((a) => {
            const isActive = a.id === current.id
            return (
              <button
                key={a.id}
                onClick={() => { onSelect(a); setOpen(false) }}
                className={
                  'w-full text-left px-3 py-1.5 text-xs transition-colors ' +
                  (isActive
                    ? 'text-amber-300 bg-amber-500/10'
                    : 'text-white/80 hover:bg-white/5')
                }
              >
                {a.label}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

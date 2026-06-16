import { useEffect, useRef, type ReactNode } from 'react'
import { T, motion } from './tokens'

// Drawer is a right-slide panel. State (open/closed, body content) is owned by
// the calling view — Drawer only handles layout, focus, Esc, and backdrop.
export default function Drawer({
  open, onClose, title, children,
}: {
  open: boolean
  onClose: () => void
  title?: ReactNode
  children?: ReactNode
}) {
  const panelRef = useRef<HTMLDivElement>(null)
  const previouslyFocused = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!open) return
    previouslyFocused.current = document.activeElement as HTMLElement | null
    panelRef.current?.focus()
    function onKey(e: KeyboardEvent) { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      previouslyFocused.current?.focus?.()
    }
  }, [open, onClose])

  return (
    <>
      <div
        onClick={onClose}
        aria-hidden
        className="fixed inset-0 z-40"
        style={{
          background: 'rgba(0,0,0,0.35)',
          backdropFilter: 'blur(8px)',
          opacity: open ? 1 : 0,
          pointerEvents: open ? 'auto' : 'none',
          transition: motion.backdrop,
        }}
      />
      <div
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-modal
        className="fixed top-0 right-0 h-full z-50 flex flex-col outline-none"
        style={{
          width: 480,
          background: T.surface,
          borderLeft: `1px solid ${T.border}`,
          transform: open ? 'translateX(0)' : 'translateX(100%)',
          transition: motion.drawer,
        }}
      >
        <div className="flex items-start justify-between px-5 py-4 border-b" style={{ borderColor: T.border }}>
          <div className="min-w-0 flex-1" style={{ color: T.textBright }}>{title}</div>
          <button
            onClick={onClose}
            aria-label="Close"
            className="text-sm px-2 py-1 rounded hover:bg-white/5"
            style={{ color: T.textDim }}
          >✕</button>
        </div>
        <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>
      </div>
    </>
  )
}

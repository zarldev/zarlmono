import { useState, useRef, useEffect, useCallback } from 'react'
import type { ReactNode, PointerEvent } from 'react'
import { PanelShell } from './PanelShell'

type Corner = 'tl' | 'tr' | 'bl' | 'br'
type Pos = { x: number; y: number }

interface FloatingPanelProps {
  id: string
  title: string
  isOpen: boolean
  onClose: () => void
  defaultCorner?: Corner
  width?: number
  maxHeight?: string
  accessory?: ReactNode
  footer?: ReactNode
  children: ReactNode
}

// Module-level z-counter — any panel bumps it on focus so it comes to
// the front of its peer group. Starts below the ResultStage band so a
// centered result always overlays a workspace panel.
let NEXT_Z = 35
function bumpZ(): number {
  NEXT_Z = Math.min(NEXT_Z + 1, 95)
  return NEXT_Z
}

const CORNER_MARGIN = 24

function resolveCorner(corner: Corner, width: number, height: number): Pos {
  const vw = window.innerWidth
  const vh = window.innerHeight
  switch (corner) {
    case 'tl': return { x: CORNER_MARGIN, y: CORNER_MARGIN }
    case 'tr': return { x: vw - width - CORNER_MARGIN, y: CORNER_MARGIN }
    case 'bl': return { x: CORNER_MARGIN, y: vh - height - CORNER_MARGIN }
    case 'br': return { x: vw - width - CORNER_MARGIN, y: vh - height - CORNER_MARGIN }
  }
}

function storageKey(id: string): string {
  return `zarl.panel.${id}.pos`
}

function loadStoredPos(id: string): Pos | null {
  try {
    const raw = window.localStorage.getItem(storageKey(id))
    if (!raw) return null
    const parsed = JSON.parse(raw) as Pos
    if (typeof parsed.x !== 'number' || typeof parsed.y !== 'number') return null
    return parsed
  } catch {
    return null
  }
}

function saveStoredPos(id: string, pos: Pos): void {
  try {
    window.localStorage.setItem(storageKey(id), JSON.stringify(pos))
  } catch {
    // localStorage can be disabled (private mode, cookie settings) —
    // silently skip; the panel still works within this session.
  }
}

function clampPos(pos: Pos, width: number, height: number): Pos {
  const vw = window.innerWidth
  const vh = window.innerHeight
  const minVisible = 48 // keep at least this many px of the panel reachable
  return {
    x: Math.min(Math.max(pos.x, minVisible - width), vw - minVisible),
    y: Math.min(Math.max(pos.y, minVisible - height), vh - minVisible),
  }
}

// FloatingPanel is a draggable workspace panel. Use it for user-toggled
// debug surfaces (camera preview, thinking stream, tool calls). Caller
// owns the open/closed state; FloatingPanel handles position, drag,
// focus-to-front, and persistence.
export function FloatingPanel({
  id,
  title,
  isOpen,
  onClose,
  defaultCorner = 'br',
  width = 256,
  maxHeight,
  accessory,
  footer,
  children,
}: FloatingPanelProps) {
  const shellRef = useRef<HTMLDivElement | null>(null)
  const dragRef = useRef<{ startX: number; startY: number; originX: number; originY: number; pointerId: number } | null>(null)
  const [pos, setPos] = useState<Pos | null>(() => loadStoredPos(id))
  const [zIndex, setZIndex] = useState<number>(() => bumpZ())

  // First-mount fallback: if nothing in localStorage, place at the
  // requested corner using the measured element height. Runs once the
  // shell is in the DOM so we have a real height.
  useEffect(() => {
    if (!isOpen || pos !== null) return
    const el = shellRef.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    setPos(resolveCorner(defaultCorner, rect.width, rect.height))
  }, [isOpen, pos, defaultCorner])

  // Re-clamp if the viewport shrinks below the stored position.
  useEffect(() => {
    function onResize() {
      setPos((prev) => {
        if (!prev) return prev
        const el = shellRef.current
        if (!el) return prev
        const rect = el.getBoundingClientRect()
        return clampPos(prev, rect.width, rect.height)
      })
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const onHeaderPointerDown = useCallback((e: PointerEvent<HTMLDivElement>) => {
    if (!pos) return
    setZIndex(bumpZ())
    dragRef.current = {
      startX: e.clientX,
      startY: e.clientY,
      originX: pos.x,
      originY: pos.y,
      pointerId: e.pointerId,
    }
    e.currentTarget.setPointerCapture(e.pointerId)
  }, [pos])

  const onHeaderPointerMove = useCallback((e: PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== e.pointerId) return
    const el = shellRef.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    const next = clampPos(
      { x: drag.originX + (e.clientX - drag.startX), y: drag.originY + (e.clientY - drag.startY) },
      rect.width,
      rect.height,
    )
    setPos(next)
  }, [])

  const onHeaderPointerUp = useCallback((e: PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== e.pointerId) return
    dragRef.current = null
    try { e.currentTarget.releasePointerCapture(e.pointerId) } catch { /* already released */ }
    setPos((prev) => {
      if (prev) saveStoredPos(id, prev)
      return prev
    })
  }, [id])

  const onPanelPointerDown = useCallback(() => {
    setZIndex(bumpZ())
  }, [])

  if (!isOpen) return null

  const visible = pos !== null

  return (
    <PanelShell
      ref={shellRef}
      title={title}
      onClose={onClose}
      accessory={accessory}
      footer={footer}
      width={width}
      maxHeight={maxHeight}
      draggable
      onHeaderPointerDown={onHeaderPointerDown}
      onHeaderPointerMove={onHeaderPointerMove}
      onHeaderPointerUp={onHeaderPointerUp}
      onPanelPointerDown={onPanelPointerDown}
      style={{
        position: 'fixed',
        left: pos?.x ?? 0,
        top: pos?.y ?? 0,
        zIndex,
        // Hide until we've resolved the initial position so the panel
        // doesn't flash at (0,0) before the corner fallback runs.
        visibility: visible ? 'visible' : 'hidden',
      }}
      className="shadow-2xl"
    >
      {children}
    </PanelShell>
  )
}

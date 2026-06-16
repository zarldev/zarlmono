import type { ReactNode, PointerEvent, CSSProperties } from 'react'
import { forwardRef } from 'react'

interface PanelShellProps {
  title: string
  onClose: () => void
  accessory?: ReactNode
  footer?: ReactNode
  children: ReactNode
  width?: number | string
  maxHeight?: string
  draggable?: boolean
  onHeaderPointerDown?: (e: PointerEvent<HTMLDivElement>) => void
  onHeaderPointerMove?: (e: PointerEvent<HTMLDivElement>) => void
  onHeaderPointerUp?: (e: PointerEvent<HTMLDivElement>) => void
  onPanelPointerDown?: (e: PointerEvent<HTMLDivElement>) => void
  style?: CSSProperties
  className?: string
}

// PanelShell is the shared chrome for all operator-readout panels
// (floating workspace panels + centered result stages). It renders
// corner brackets, a lowercase mono header with optional accessory and
// close button, the body, and an optional footer row. Positioning and
// drag behaviour live in the caller — this is purely the visual skin.
export const PanelShell = forwardRef<HTMLDivElement, PanelShellProps>(function PanelShell(
  { title, onClose, accessory, footer, children, width, maxHeight, draggable = false, onHeaderPointerDown, onHeaderPointerMove, onHeaderPointerUp, onPanelPointerDown, style, className = '' },
  ref,
) {
  return (
    <div
      ref={ref}
      onPointerDown={onPanelPointerDown}
      className={`op-brackets op-panel op-tune-in flex flex-col ${className}`}
      style={{ width, maxHeight, ...style }}
    >
      <span className="op-b-tl" aria-hidden />
      <span className="op-b-tr" aria-hidden />
      <span className="op-b-bl" aria-hidden />
      <span className="op-b-br" aria-hidden />

      <div
        onPointerDown={onHeaderPointerDown}
        onPointerMove={onHeaderPointerMove}
        onPointerUp={onHeaderPointerUp}
        onPointerCancel={onHeaderPointerUp}
        className={`flex items-center justify-between gap-2 px-2.5 py-1.5 border-b border-white/[0.07] shrink-0 ${draggable ? 'cursor-grab select-none touch-none active:cursor-grabbing' : ''}`}
      >
        <div className="flex items-center gap-1.5 min-w-0 op-mono text-[10px] text-white/55">
          {draggable && (
            <span className="inline-flex flex-col gap-[2px] mr-1 opacity-50 shrink-0" aria-hidden>
              <span className="flex gap-[2px]"><span className="w-[2px] h-[2px] bg-white/60" /><span className="w-[2px] h-[2px] bg-white/60" /></span>
              <span className="flex gap-[2px]"><span className="w-[2px] h-[2px] bg-white/60" /><span className="w-[2px] h-[2px] bg-white/60" /></span>
              <span className="flex gap-[2px]"><span className="w-[2px] h-[2px] bg-white/60" /><span className="w-[2px] h-[2px] bg-white/60" /></span>
            </span>
          )}
          <span className="truncate">{title}</span>
          {accessory && (
            <>
              <span className="text-white/20 shrink-0">·</span>
              <span className="flex items-center gap-1.5 min-w-0">{accessory}</span>
            </>
          )}
        </div>
        <button
          onClick={onClose}
          onPointerDown={(e) => e.stopPropagation()}
          className="op-mono text-[10px] text-white/40 hover:text-white/90 transition-colors shrink-0"
          title="Close"
          aria-label="Close"
        >
          esc
        </button>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden flex flex-col">
        {children}
      </div>

      {footer && (
        <div className="flex items-center justify-between px-2.5 py-1 border-t border-white/[0.07] op-mono text-[10px] text-white/35 shrink-0">
          {footer}
        </div>
      )}
    </div>
  )
})

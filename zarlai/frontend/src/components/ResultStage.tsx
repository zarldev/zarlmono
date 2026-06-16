import type { ReactNode } from 'react'
import { PanelShell } from './PanelShell'

interface ResultStageProps {
  title: string
  isOpen: boolean
  onClose: () => void
  width?: number | string
  maxHeight?: string
  accessory?: ReactNode
  footer?: ReactNode
  children: ReactNode
  // stackOffset lets callers cascade multiple concurrent stages so they
  // don't pile up on top of each other — pass the index of this stage
  // in the currently-open set. Defaults to 0.
  stackOffset?: number
}

// ResultStage presents an assistant-pushed result (chart, findings,
// report, search hits) as a centered card on top of the chamber. The
// host view owns ESC handling so a consistent dismiss-priority order
// applies across concurrently-open stages; ResultStage only renders the
// header's own esc button. Stages cascade 24px down-right per
// stackOffset so concurrent results don't fully overlap.
export function ResultStage({
  title,
  isOpen,
  onClose,
  width = 540,
  maxHeight = '80vh',
  accessory,
  footer,
  children,
  stackOffset = 0,
}: ResultStageProps) {
  if (!isOpen) return null

  const offsetPx = stackOffset * 24

  return (
    <PanelShell
      title={title}
      onClose={onClose}
      accessory={accessory}
      footer={footer}
      width={width}
      maxHeight={maxHeight}
      style={{
        position: 'fixed',
        left: '50%',
        top: '50%',
        transform: `translate(calc(-50% + ${offsetPx}px), calc(-50% + ${offsetPx}px))`,
        zIndex: 100 + stackOffset,
      }}
      className="shadow-2xl"
    >
      {children}
    </PanelShell>
  )
}

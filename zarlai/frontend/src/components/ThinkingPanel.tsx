import ReactMarkdown from 'react-markdown'
import type { ThinkingEntry } from '@/hooks/usePresenceSession'
import { FloatingPanel } from './FloatingPanel'

interface Props {
  entry: ThinkingEntry | null
  isOpen: boolean
  onClose: () => void
}

// ThinkingPanel is an opt-in side view of the LLM's internal reasoning
// for the most recent turn. It's never shown by default — the toggle on
// the main UI surfaces it only when the user explicitly asks for it.
export default function ThinkingPanel({ entry, isOpen, onClose }: Props) {
  return (
    <FloatingPanel
      id="thinking"
      title="thinking"
      isOpen={isOpen}
      onClose={onClose}
      defaultCorner="tl"
      width={380}
      maxHeight="70vh"
    >
      {entry ? (
        <article className="flex-1 overflow-y-auto p-3 text-[11px] leading-relaxed text-white/70 space-y-1.5 font-mono min-h-0">
          <ReactMarkdown
            components={{
              p: ({ children }) => <p className="leading-relaxed">{children}</p>,
              strong: ({ children }) => <strong className="text-white/90">{children}</strong>,
              em: ({ children }) => <em className="text-white/90 italic">{children}</em>,
              code: ({ children }) => (
                <code className="text-[#fcd34d] bg-white/5 px-1 py-0.5 rounded">{children}</code>
              ),
            }}
          >
            {entry.content}
          </ReactMarkdown>
        </article>
      ) : (
        <p className="text-[11px] text-white/40 p-4">no thinking captured yet for this turn.</p>
      )}
    </FloatingPanel>
  )
}

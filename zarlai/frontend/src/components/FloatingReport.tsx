import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { ReportSpec } from '@/hooks/usePresenceSession'
import { FloatingPanel } from './FloatingPanel'

interface Props {
  spec: ReportSpec | null
  onDismiss: () => void
}

// FloatingReport is the post-completion sibling of FloatingFindings —
// a long markdown task report in a draggable workspace panel so the
// user can park it alongside the avatar and keep chatting while
// reading. GFM-enabled so pipe tables and task-lists render.
export default function FloatingReport({ spec, onDismiss }: Props) {
  const isOpen = !!spec

  return (
    <FloatingPanel
      id="report"
      title={spec ? `report · ${spec.title.toLowerCase()}` : 'report'}
      isOpen={isOpen}
      onClose={onDismiss}
      defaultCorner="br"
      width={560}
      maxHeight="85vh"
      footer={
        spec?.obsidian_path ? (
          <>
            <span>saved →</span>
            <span className="text-white/55 truncate" title={spec.obsidian_path}>{spec.obsidian_path}</span>
          </>
        ) : undefined
      }
    >
      {spec && (
        <article className="flex-1 overflow-y-auto px-4 py-3 text-[13px] leading-relaxed text-white/85 space-y-2 min-h-0">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              h1: ({ children }) => <h1 className="text-[15px] font-semibold text-white mt-3 mb-1">{children}</h1>,
              h2: ({ children }) => <h2 className="text-[13px] font-semibold text-white mt-3 mb-1">{children}</h2>,
              h3: ({ children }) => <h3 className="text-[12px] font-semibold text-white/90 mt-2 mb-0.5">{children}</h3>,
              p:  ({ children }) => <p className="leading-relaxed">{children}</p>,
              ul: ({ children }) => <ul className="list-disc pl-5 space-y-1 marker:text-white/30">{children}</ul>,
              ol: ({ children }) => <ol className="list-decimal pl-5 space-y-1 marker:text-white/30">{children}</ol>,
              li: ({ children }) => <li className="leading-relaxed">{children}</li>,
              strong: ({ children }) => <strong className="text-white font-semibold">{children}</strong>,
              em: ({ children }) => <em className="italic text-white/95">{children}</em>,
              blockquote: ({ children }) => (
                <blockquote className="border-l-2 border-white/15 pl-3 text-white/70 italic">{children}</blockquote>
              ),
              table: ({ children }) => (
                <div className="overflow-x-auto my-2 -mx-1">
                  <table className="w-full text-[12px] border-collapse">{children}</table>
                </div>
              ),
              thead: ({ children }) => <thead className="border-b border-white/15">{children}</thead>,
              tbody: ({ children }) => <tbody>{children}</tbody>,
              tr: ({ children }) => (
                <tr className="border-b border-white/5 last:border-b-0">{children}</tr>
              ),
              th: ({ children }) => (
                <th className="text-left px-2 py-1.5 font-semibold text-white/90 whitespace-nowrap">{children}</th>
              ),
              td: ({ children }) => (
                <td className="px-2 py-1.5 align-top text-white/80">{children}</td>
              ),
              code: ({ children, ...props }) => {
                const isBlock = props.className?.includes('language-')
                if (isBlock) return <code className="block text-[12px]">{children}</code>
                return <code className="text-[#fcd34d] bg-white/5 px-1 py-0.5 rounded font-mono text-[12px]">{children}</code>
              },
              pre: ({ children }) => (
                <pre className="bg-black/40 border border-white/5 rounded p-2 overflow-x-auto text-[12px] font-mono">{children}</pre>
              ),
              a: ({ href, children }) => (
                <a href={href} target="_blank" rel="noopener noreferrer" className="text-[#93c5fd] hover:underline">{children}</a>
              ),
              hr: () => <hr className="border-white/10 my-3" />,
            }}
          >
            {spec.markdown}
          </ReactMarkdown>
        </article>
      )}
    </FloatingPanel>
  )
}

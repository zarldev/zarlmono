import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { T } from './tokens'

// Shared Markdown renderer for admin-v2. Palette and component map mirror
// FloatingReport.tsx but use the admin-v2 token set so prompts and prefixes
// render with the same look across views.
export default function Markdown({ source }: { source: string }) {
  return (
    <div className="text-[13px] leading-relaxed" style={{ color: T.text }}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => <h1 className="text-[20px] font-semibold mt-4 mb-2" style={{ color: T.textBright }}>{children}</h1>,
          h2: ({ children }) => <h2 className="text-[16px] font-semibold mt-4 mb-2" style={{ color: T.textBright }}>{children}</h2>,
          h3: ({ children }) => <h3 className="text-[14px] font-semibold mt-3 mb-1.5" style={{ color: T.textBright }}>{children}</h3>,
          p:  ({ children }) => <p className="my-2">{children}</p>,
          ul: ({ children }) => <ul className="list-disc pl-5 my-2 space-y-1 marker:text-white/30">{children}</ul>,
          ol: ({ children }) => <ol className="list-decimal pl-5 my-2 space-y-1 marker:text-white/30">{children}</ol>,
          li: ({ children }) => <li>{children}</li>,
          strong: ({ children }) => <strong className="font-semibold" style={{ color: T.textBright }}>{children}</strong>,
          em: ({ children }) => <em className="italic">{children}</em>,
          blockquote: ({ children }) => (
            <blockquote className="border-l-2 pl-3 italic my-2" style={{ borderColor: T.borderStrong, color: T.textDim }}>{children}</blockquote>
          ),
          code: ({ children, ...props }) => {
            const isBlock = props.className?.includes('language-')
            if (isBlock) return <code className="block text-[12px]">{children}</code>
            return <code className="px-1 py-0.5 rounded font-mono text-[12px]" style={{ background: 'rgba(255,255,255,0.06)', color: '#fcd34d' }}>{children}</code>
          },
          pre: ({ children }) => (
            <pre className="rounded p-3 overflow-x-auto text-[12px] font-mono my-2"
                 style={{ background: 'rgba(0,0,0,0.4)', border: `1px solid ${T.border}` }}>{children}</pre>
          ),
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noopener noreferrer" className="hover:underline" style={{ color: T.accent.runtime }}>{children}</a>
          ),
          hr: () => <hr className="my-4" style={{ borderColor: T.border }} />,
          table: ({ children }) => <table className="my-3 border-collapse text-[12px]">{children}</table>,
          th: ({ children }) => <th className="text-left font-semibold px-2 py-1 border" style={{ borderColor: T.border, color: T.textBright }}>{children}</th>,
          td: ({ children }) => <td className="px-2 py-1 border" style={{ borderColor: T.border }}>{children}</td>,
        }}
      >
        {source}
      </ReactMarkdown>
    </div>
  )
}

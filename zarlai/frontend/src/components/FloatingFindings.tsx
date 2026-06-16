import { useEffect, useMemo, useState } from 'react'
import type { FindingsSpec, FindingItem } from '@/hooks/usePresenceSession'
import { FloatingPanel } from './FloatingPanel'

interface Props {
  spec: FindingsSpec | null
  onDismiss: () => void
}

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return ''
  }
}

const YOUTUBE_ID_RE = /^[A-Za-z0-9_-]{11}$/

// Returns the 11-char video ID when the URL is a YouTube video link,
// else null.
function youtubeIdOf(raw: string): string | null {
  try {
    const u = new URL(raw)
    const host = u.hostname.toLowerCase().replace(/^www\./, '')
    if (host === 'youtu.be') {
      const id = u.pathname.replace(/^\//, '').split('/')[0]
      return YOUTUBE_ID_RE.test(id) ? id : null
    }
    if (host === 'youtube.com' || host === 'm.youtube.com' || host === 'music.youtube.com') {
      const v = u.searchParams.get('v') ?? ''
      if (YOUTUBE_ID_RE.test(v)) return v
      const parts = u.pathname.replace(/^\//, '').split('/')
      if (parts.length >= 2 && ['embed', 'shorts', 'live', 'v'].includes(parts[0])) {
        return YOUTUBE_ID_RE.test(parts[1]) ? parts[1] : null
      }
    }
    return null
  } catch {
    return null
  }
}

// FloatingFindings hosts assistant-pushed search results in a draggable
// workspace panel — so the user can move a playing video aside and
// keep chatting. When a YouTube video is active it dominates the panel
// body and the sibling list collapses behind a toggle; when no video
// is present (e.g. plain web-search hits) the list is the whole view.
export default function FloatingFindings({ spec, onDismiss }: Props) {
  const isOpen = !!spec
  const [activeYtId, setActiveYtId] = useState<string | null>(null)
  const [listExpanded, setListExpanded] = useState(false)

  const ytByIndex = useMemo(() => {
    if (!spec) return new Map<number, string>()
    const m = new Map<number, string>()
    spec.items.forEach((it, i) => {
      const id = youtubeIdOf(it.url)
      if (id) m.set(i, id)
    })
    return m
  }, [spec])

  useEffect(() => {
    if (!spec) {
      setActiveYtId(null)
      setListExpanded(false)
      return
    }
    const firstYt = spec.items.map((it) => youtubeIdOf(it.url)).find((v): v is string => !!v)
    setActiveYtId(firstYt ?? null)
    // Auto-collapse the list when a video takes over; show the list
    // up-front only when there's nothing to play.
    setListExpanded(!firstYt)
  }, [spec])

  if (!spec) return null

  const hasPlayer = activeYtId !== null
  const itemCount = spec.items.length
  const otherCount = hasPlayer ? itemCount - 1 : itemCount

  return (
    <FloatingPanel
      id="findings"
      title={`findings · ${spec.title.toLowerCase()}`}
      isOpen={isOpen}
      onClose={onDismiss}
      defaultCorner="tr"
      width={hasPlayer ? 460 : 520}
      maxHeight="85vh"
      accessory={<span className="text-white/40 tabular-nums">{itemCount.toString().padStart(2, '0')}</span>}
    >
      <div className="flex-1 flex flex-col min-h-0">
        {hasPlayer && (
          <div className="shrink-0 bg-black aspect-video">
            <iframe
              key={activeYtId}
              src={`https://www.youtube-nocookie.com/embed/${activeYtId}?autoplay=1&rel=0`}
              title="YouTube player"
              allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture"
              allowFullScreen
              className="w-full h-full border-0"
            />
          </div>
        )}

        {hasPlayer && otherCount > 0 && (
          <button
            onClick={() => setListExpanded((v) => !v)}
            className="shrink-0 flex items-center justify-between px-2.5 py-1.5 border-b border-white/[0.07] bg-white/[0.015] op-mono text-[10px] text-white/55 hover:text-white transition-colors"
          >
            <span>{otherCount} more · {listExpanded ? 'hide' : 'show'}</span>
            <span className={`transition-transform duration-200 ${listExpanded ? 'rotate-180' : ''}`} aria-hidden>▾</span>
          </button>
        )}

        {(!hasPlayer || listExpanded) && (
          <ol className="flex-1 overflow-y-auto p-2 space-y-1.5 min-h-0">
            {spec.items.map((it, i) =>
              renderItem(it, i, ytByIndex.get(i) ?? null, activeYtId, setActiveYtId),
            )}
          </ol>
        )}
      </div>
    </FloatingPanel>
  )
}

function renderItem(
  it: FindingItem,
  i: number,
  ytId: string | null,
  activeYtId: string | null,
  setActiveYtId: (id: string) => void,
) {
  const host = hostOf(it.url)
  const isActive = ytId !== null && ytId === activeYtId

  if (ytId) {
    return (
      <li key={i}>
        <button
          onClick={() => setActiveYtId(ytId)}
          className={
            'w-full text-left p-2 rounded flex gap-2 transition-colors ' +
            (isActive
              ? 'bg-amber-500/10 border border-amber-500/40'
              : 'border border-white/5 bg-white/[0.02] hover:bg-white/[0.06] hover:border-white/10')
          }
        >
          <img
            src={`https://i.ytimg.com/vi/${ytId}/mqdefault.jpg`}
            alt=""
            className="w-20 h-12 rounded object-cover shrink-0 bg-black/40"
            loading="lazy"
          />
          <div className="flex-1 min-w-0">
            <div className={`text-xs leading-snug line-clamp-2 ${isActive ? 'text-amber-200' : 'text-white/85'}`}>
              {it.title}
            </div>
            {it.summary && (
              <div className="text-[10px] text-white/40 mt-1 line-clamp-2">{it.summary}</div>
            )}
          </div>
        </button>
      </li>
    )
  }

  return (
    <li key={i} className="group">
      <a
        href={it.url}
        target="_blank"
        rel="noopener noreferrer"
        className="block p-3 rounded border border-white/5 bg-white/[0.02] hover:bg-white/[0.06] hover:border-white/10 transition-colors"
      >
        <div className="flex items-baseline gap-2">
          <span className="text-[10px] text-white/30 tabular-nums w-5 shrink-0">{i + 1}.</span>
          <div className="flex-1 min-w-0">
            <div className="text-sm text-white/90 group-hover:text-white leading-snug">{it.title}</div>
            {it.summary && (
              <div className="text-xs text-white/50 mt-1 leading-snug line-clamp-3">{it.summary}</div>
            )}
            <div className="op-mono text-[10px] text-white/30 mt-1.5 flex items-center gap-2">
              {it.source && <span>{it.source}</span>}
              {it.source && host && <span className="text-white/20">·</span>}
              {host && <span className="truncate">{host}</span>}
            </div>
          </div>
        </div>
      </a>
    </li>
  )
}

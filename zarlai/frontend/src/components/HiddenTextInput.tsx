// frontend/src/components/HiddenTextInput.tsx
import { useEffect, useRef, useState } from 'react'

interface Props {
  visible: boolean
  onSend: (text: string) => void
  onDismiss: () => void
  onAttachFiles?: (files: File[]) => Promise<number> | number
}

export default function HiddenTextInput({ visible, onSend, onDismiss, onAttachFiles }: Props) {
  const [text, setText] = useState('')
  const [dragActive, setDragActive] = useState(false)
  const [stagedCount, setStagedCount] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (visible) inputRef.current?.focus()
    else { setStagedCount(0); setDragActive(false) }
  }, [visible])

  if (!visible) return null

  const handleFiles = async (list: FileList | null) => {
    if (!list || !onAttachFiles) return
    const images = Array.from(list).filter((f) => f.type.startsWith('image/'))
    if (!images.length) return
    const added = await onAttachFiles(images)
    setStagedCount((n) => n + (typeof added === 'number' ? added : images.length))
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (text.trim()) { onSend(text.trim()); setText(''); setStagedCount(0) }
      }}
      onDragEnter={(e) => { e.preventDefault(); e.stopPropagation(); setDragActive(true) }}
      onDragOver={(e) => { e.preventDefault(); e.stopPropagation(); setDragActive(true) }}
      onDragLeave={(e) => {
        e.preventDefault(); e.stopPropagation()
        if (e.currentTarget.contains(e.relatedTarget as Node)) return
        setDragActive(false)
      }}
      onDrop={(e) => {
        e.preventDefault(); e.stopPropagation(); e.nativeEvent.stopImmediatePropagation()
        setDragActive(false)
        void handleFiles(e.dataTransfer?.files ?? null)
      }}
      className="fixed left-1/2 -translate-x-1/2 bottom-10 z-30 w-[480px]"
    >
      <div className="relative">
        <input
          ref={inputRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onPaste={(e) => {
            const items = Array.from(e.clipboardData?.items ?? [])
            const files = items
              .filter((it) => it.kind === 'file' && it.type.startsWith('image/'))
              .map((it) => it.getAsFile())
              .filter((f): f is File => !!f)
            if (files.length && onAttachFiles) {
              e.preventDefault(); e.stopPropagation(); e.nativeEvent.stopImmediatePropagation()
              ;(async () => {
                const added = await onAttachFiles(files)
                setStagedCount((n) => n + (typeof added === 'number' ? added : files.length))
              })()
            }
          }}
          onKeyDown={(e) => { if (e.key === 'Escape') onDismiss() }}
          placeholder={dragActive ? 'drop image to attach…' : 'speak, type, or drop an image…'}
          className={
            'w-full px-4 py-3 rounded-full bg-black/40 backdrop-blur-md border text-sm text-white/90 placeholder-white/30 outline-none transition-colors ' +
            (dragActive
              ? 'border-amber-300/60 ring-2 ring-amber-300/30'
              : 'border-white/10 focus:border-white/25')
          }
        />
        {stagedCount > 0 && (
          <span
            className="absolute right-3 top-1/2 -translate-y-1/2 px-2 py-0.5 rounded-full bg-amber-500/20 text-amber-200 text-[10px] uppercase tracking-wider"
            title={`${stagedCount} image${stagedCount === 1 ? '' : 's'} attached`}
          >
            {stagedCount} img
          </span>
        )}
      </div>
    </form>
  )
}

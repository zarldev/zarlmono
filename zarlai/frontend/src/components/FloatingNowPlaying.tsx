import { memo } from 'react'
import { FloatingPanel } from './FloatingPanel'
import type { NowPlayingInfo } from './NowPlaying'

interface Props {
  info: NowPlayingInfo | null
  isOpen: boolean
  onClose: () => void
}

function FloatingNowPlayingImpl({ info, isOpen, onClose }: Props) {
  const open = isOpen && !!info?.track
  const state = info?.is_playing ? 'playing' : 'paused'

  return (
    <FloatingPanel
      id="now-playing"
      title="deck a"
      isOpen={open}
      onClose={onClose}
      defaultCorner="bl"
      width={320}
      accessory={
        <span className="flex items-center gap-1.5">
          <span className="text-white/45">now {state}</span>
          {info?.device && (
            <>
              <span className="text-white/20">·</span>
              <span className="text-white/35 truncate max-w-[14ch]">{info.device}</span>
            </>
          )}
        </span>
      }
    >
      {info && (
        <div className="flex items-center gap-3 px-3 py-2">
          {info.image_url ? (
            <div className="relative shrink-0">
              <span className="absolute -inset-[3px] border border-[#f59e0b]/40 pointer-events-none" aria-hidden />
              <img
                src={info.image_url}
                alt=""
                aria-hidden
                className="relative block w-11 h-11 object-cover bg-black/40"
              />
            </div>
          ) : (
            <div className="shrink-0 w-11 h-11 border border-white/10 flex items-center justify-center op-mono text-[10px] text-white/30">
              no art
            </div>
          )}
          <div className="flex flex-col min-w-0 gap-1 flex-1">
            <span className="font-medium text-white text-[13px] leading-tight truncate">
              {info.track}
            </span>
            <span className="text-white/50 text-[11.5px] leading-tight truncate">
              {info.artist}
            </span>
          </div>
          <span className={`op-eq shrink-0 ${info.is_playing ? 'is-playing' : 'is-paused'}`}>
            <span />
            <span />
            <span />
          </span>
        </div>
      )}
    </FloatingPanel>
  )
}

export const FloatingNowPlaying = memo(FloatingNowPlayingImpl)

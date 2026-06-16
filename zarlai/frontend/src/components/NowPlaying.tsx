import { memo } from 'react'

export type NowPlayingInfo = {
  track: string
  artist: string
  album?: string
  device?: string
  image_url?: string
  is_playing: boolean
}

type Props = {
  info: NowPlayingInfo | null
}

// OPERATOR READOUT · NowPlaying — a thin "deck" readout pinned to the
// top of the immersive chamber. Mono micro-label, album-art tile with
// a trompe-l'oeil offset frame, track/artist in the body voice, and a
// 3-bar equalizer that animates on playback.
//
// Hidden when the backend reports no active track (fresh install,
// signed-out account, or nothing queued).
function NowPlayingImpl({ info }: Props) {
  if (!info || !info.track) return null

  const state = info.is_playing ? 'playing' : 'paused'
  const tooltip = info.album
    ? `${info.track} — ${info.artist} (${info.album})${info.device ? ` · ${info.device}` : ''}`
    : `${info.track} — ${info.artist}${info.device ? ` · ${info.device}` : ''}`

  return (
    <div
      className="fixed bottom-6 left-6 z-20 op-tune-in"
      title={tooltip}
    >
      <div className="op-brackets op-panel flex items-center gap-3 px-3 py-2 rounded-sm">
        <span className="op-b-tl" />
        <span className="op-b-tr" />
        <span className="op-b-bl" />
        <span className="op-b-br" />

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

        <div className="flex flex-col min-w-0 gap-1">
          <div className="flex items-center gap-1.5 op-mono text-[10px]">
            <span className="text-[#f59e0b]/85">deck a</span>
            <span className="text-white/20">·</span>
            <span className="text-white/45">now {state}</span>
            {info.device && (
              <>
                <span className="text-white/20">·</span>
                <span className="text-white/35 truncate max-w-[14ch]">{info.device}</span>
              </>
            )}
          </div>
          <div className="flex items-baseline gap-2 min-w-0 overflow-hidden">
            <span className="font-medium text-white text-[13px] leading-tight truncate max-w-[24ch]">
              {info.track}
            </span>
            <span className="text-white/50 text-[11.5px] leading-tight truncate max-w-[18ch]">
              {info.artist}
            </span>
          </div>
        </div>

        <span className={`op-eq shrink-0 ml-1 ${info.is_playing ? 'is-playing' : 'is-paused'}`}>
          <span />
          <span />
          <span />
        </span>
      </div>
    </div>
  )
}

export const NowPlaying = memo(NowPlayingImpl)

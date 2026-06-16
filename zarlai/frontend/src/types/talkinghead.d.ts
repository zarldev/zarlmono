declare module '@met4citizen/talkinghead' {
  export interface TalkingHeadOptions {
    ttsEndpoint?: string | null
    ttsApikey?: string | null
    ttsLang?: string
    ttsVoice?: string
    ttsRate?: number
    ttsPitch?: number
    ttsVolume?: number
    lipsyncModules?: string[]
    lipsyncLang?: string
    modelPixelRatio?: number
    modelFPS?: number
    cameraView?: 'full' | 'mid' | 'upper' | 'head'
    cameraDistance?: number
    cameraX?: number
    cameraY?: number
    cameraRotateEnable?: boolean
    cameraPanEnable?: boolean
    cameraZoomEnable?: boolean
    avatarMood?: string
    avatarMute?: boolean
    lightAmbientColor?: number | string
    lightAmbientIntensity?: number
    lightDirectColor?: number | string
    lightDirectIntensity?: number
    [key: string]: unknown
  }

  export interface ShowAvatarOptions {
    url: string
    body?: 'M' | 'F'
    lipsyncLang?: string
    avatarMood?: string
    avatarMute?: boolean
    ttsLang?: string
    ttsVoice?: string
    [key: string]: unknown
  }

  /** Internal morph target entry used by the library's animate loop. */
  export interface MorphTargetEntry {
    /** When non-null the library uses this value directly each frame, bypassing
     *  keyframe/tween logic. Set to null to return to baseline (0). */
    realtime: number | null
    /** MUST be set to true every time we mutate realtime/fixed/etc. or the
     *  library's animate() skips the morph entirely. */
    needsUpdate: boolean
    /** Currently applied value (read-only from our side). */
    applied: number
    /** Baseline resting value (0 for most morph targets). */
    baseline: number
    [key: string]: unknown
  }

  export class TalkingHead {
    constructor(element: HTMLElement, options?: TalkingHeadOptions)
    showAvatar(config: ShowAvatarOptions, onprogress?: ((progress: number) => void) | null): Promise<void>
    setMood(mood: string): void
    setView(view: 'full' | 'mid' | 'upper' | 'head', opt?: Record<string, unknown>): void
    speakText(text: string, opt?: Record<string, unknown>): void
    speakAudio(audio: unknown, opt?: Record<string, unknown>): void
    streamStart(opt?: Record<string, unknown>, onAudioStart?: (() => void) | null, onAudioEnd?: (() => void) | null, onSubtitles?: ((s: string) => void) | null, onMetrics?: (() => void) | null): void
    streamAudio(audio: unknown): void
    streamNotifyEnd(): void
    streamInterrupt(): void
    streamStop(): void
    start(): void
    stop(): void
    startListening(analyzer: AnalyserNode, opt?: Record<string, unknown>, onchange?: ((state: string) => void) | null): void
    stopListening(): void
    lookAt(x: number, y: number, t: number): void
    lookAtCamera(t: number): void
    playGesture(name: string, dur?: number, mirror?: boolean, ms?: number): void
    stopGesture(ms?: number): void
    playAnimation(url: string, onprogress?: ((p: number) => void) | null, dur?: number, ndx?: number, scale?: number): Promise<void>
    stopAnimation(): void
    /** Internal morph target state keyed by blend-shape name.
     *  Set `.realtime` on a viseme entry to drive lipsync externally. */
    mtAvatar: Record<string, MorphTargetEntry>
    /** Library's gesture template registry keyed by gesture name.
     *  Values are opaque rig-keyframe records; we write into it via
     *  registerCustomGestures to extend the built-in set without forking
     *  the library. See talkingHeadGestures.ts. */
    gestureTemplates: Record<string, unknown>
  }
}

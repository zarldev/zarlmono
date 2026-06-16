// Available full-body animations. Drop a Mixamo FBX export into
// `frontend/public/models/` and add an entry here — the TalkingHead library
// drives it via showAvatar's target armature. Any Mixamo rig works; scale is
// usually 0.01 for metric units.
export interface Animation {
  id: string
  label: string
  url: string
  // Duration the animation runs before looping back to idle, in seconds.
  // 0 means "loop forever until stopAnimation is called".
  duration?: number
}

export const IDLE_ANIMATION: Animation = {
  id: 'idle',
  label: 'Idle',
  url: '/models/idle.fbx',
  duration: 0,
}

export const ANIMATIONS: Animation[] = [
  IDLE_ANIMATION,
]

export function animationById(id: string): Animation | undefined {
  return ANIMATIONS.find((a) => a.id === id)
}

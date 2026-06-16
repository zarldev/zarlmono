// Available avatars. Drop additional GLB files into `frontend/public/models/`
// and add an entry here. Avaturn exports work out of the box with the current
// Oculus-viseme lipsync pipeline; other rigs may get reduced mouth motion.
export interface Avatar {
  id: string
  label: string
  url: string
  body: 'M' | 'F'
}

// TalkingHead's showAvatar expects an RPM/Oculus-rigged GLB (Avaturn exports
// qualify). VRM-format rigs (vroid) and Three.js morph demos (facecap) don't
// load through this path; keep them out of the picker until we wire a
// separate loader for them.
export const AVATARS: Avatar[] = [
  { id: 'avaturn',   label: 'Avaturn',   url: '/models/avaturn.glb',   body: 'F' },
  { id: 'brunette',  label: 'Brunette',  url: '/models/brunette.glb',  body: 'F' },
  { id: 'avatarsdk', label: 'AvatarSDK', url: '/models/avatarsdk.glb', body: 'M' },
  { id: 'mpfb',      label: 'MPFB',      url: '/models/mpfb.glb',      body: 'M' },
]

export const DEFAULT_AVATAR = AVATARS[0]

export function avatarById(id: string): Avatar {
  return AVATARS.find((a) => a.id === id) ?? DEFAULT_AVATAR
}

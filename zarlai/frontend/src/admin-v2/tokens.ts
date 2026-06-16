// Design tokens for admin-v2. Kept as a typed const rather than Tailwind
// theme extensions so the new palette does not leak into the existing app.
export const T = {
  // Aligned to the immersive canvas so admin reads as the same world,
  // not a separate app window.
  bg: '#07090f',
  surface: '#0d0f14',
  raised: '#13161c',
  border: 'rgba(255,255,255,0.05)',
  borderStrong: 'rgba(255,255,255,0.1)',
  textBright: '#eef0f4',
  text: '#c8cad0',
  textDim: '#5a5d66',
  accent: {
    identity: '#fb7185',
    runtime:  '#38bdf8',
    review:   '#fbbf24',
    history:  '#84cc16',
  },
  status: {
    error: '#f87171',
    ok:    '#86efac',
  },
} as const

export const motion = {
  drawer:    'transform 220ms cubic-bezier(.2,.9,.2,1)',
  backdrop:  'opacity 180ms ease-out',
  sidebar:   '160ms ease-out',
  hover:     '120ms ease-out',
} as const

export type GroupId = 'identity' | 'runtime' | 'review' | 'history'
export type ViewId =
  | 'dashboard'
  | 'identity' | 'prompts' | 'faces' | 'profiles' | 'skills'
  | 'models' | 'tools' | 'tasks' | 'templates'
  | 'proposals'
  | 'chats' | 'tool-calls'

// Maps a view back to its sidebar group so the sidebar knows which item is
// active even when the view is opened via a card click from the dashboard.
export const viewGroup: Record<ViewId, GroupId | null> = {
  'dashboard':  null,
  'identity':   'identity',
  'prompts':    'identity',
  'skills':     'identity',
  'faces':      'identity',
  'profiles':   'identity',
  'models':     'runtime',
  'tools':      'runtime',
  'templates':  'runtime',
  'tasks':      'runtime',
  'proposals':  'review',
  'chats':      'history',
  'tool-calls': 'history',
}

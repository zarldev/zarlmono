import { useState } from 'react'
import Sidebar from './Sidebar'
import Dashboard from './Dashboard'
import IdentityView from './views/IdentityView'
import ToolCallsView from './views/ToolCallsView'
import TasksView from './views/TasksView'
import ChatsView from './views/ChatsView'
import ProposalsView from './views/ProposalsView'
import PromptsView from './views/PromptsView'
import FacesView from './views/FacesView'
import ProfilesView from './views/ProfilesView'
import TaskRunnerView from './views/TaskRunnerView'
import ToolsView from './views/ToolsView'
import SkillsView from './views/SkillsView'
import TemplatesView from './views/TemplatesView'
import { T, type ViewId } from './tokens'

// TITLES provides the human label shown in the canvas header per view.
const TITLES: Record<ViewId, string> = {
  'dashboard':  'Overview',
  'identity':   'Identity',
  'prompts':    'Prompts',
  'skills':     'Skills',
  'faces':      'Faces',
  'profiles':   'Profiles',
  'models':     'Task runner',
  'tools':      'Tools',
  'templates':  'Templates',
  'tasks':      'Tasks',
  'proposals':  'Proposals',
  'chats':      'Chats',
  'tool-calls': 'Tool calls',
}

function ViewContent({ view, onOpen }: { view: ViewId; onOpen: (v: ViewId) => void }) {
  switch (view) {
    case 'dashboard':  return <Dashboard onOpen={onOpen} />
    case 'identity':   return <IdentityView />
    case 'prompts':    return <PromptsView />
    case 'skills':     return <SkillsView onOpen={onOpen} />
    case 'faces':      return <FacesView />
    case 'profiles':   return <ProfilesView />
    case 'models':     return <TaskRunnerView />
    case 'tools':      return <ToolsView />
    case 'templates':  return <TemplatesView />
    case 'tasks':      return <TasksView />
    case 'proposals':  return <ProposalsView />
    case 'chats':      return <ChatsView />
    case 'tool-calls': return <ToolCallsView />
  }
}

export default function AdminV2({ onNavigate }: { onNavigate: (to: string) => void }) {
  const [view, setView] = useState<ViewId>('dashboard')

  return (
    <div className="flex min-h-screen" style={{ background: T.bg, color: T.text }}>
      <Sidebar active={view} onSelect={setView} onHome={() => setView('dashboard')} />

      <main className="flex-1 flex flex-col min-w-0">
        <header className="flex items-center justify-between px-8 py-5 border-b" style={{ borderColor: T.border }}>
          <h1 className="text-[18px] font-medium tracking-tight" style={{ color: T.textBright }}>{TITLES[view]}</h1>
          <button
            onClick={() => onNavigate('/')}
            title="Back to conversation"
            className="w-5 h-5 hover:text-white/80"
            style={{ color: 'rgba(255,255,255,0.5)', transition: 'color 160ms ease-out' }}
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="19" y1="12" x2="5" y2="12" />
              <polyline points="12 19 5 12 12 5" />
            </svg>
          </button>
        </header>

        <div className="flex-1 px-8 py-6 max-w-[1400px] w-full">
          <ViewContent view={view} onOpen={setView} />
        </div>
      </main>
    </div>
  )
}

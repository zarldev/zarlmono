import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Router from './Router'
import './App.css'

const queryClient = new QueryClient()

// Note: StrictMode disabled. The real-time audio pipeline (VAD + notification
// subscription + TTS playback) does not survive React 18's double-mount in
// development — it creates orphan subscriptions and duplicate audio streams.
// Making it strict-safe would require rewriting the hook around AbortController
// cancellation of the async init path. Follow-up if we ever need React's dev
// checks back on.
createRoot(document.getElementById('root')!).render(
  <QueryClientProvider client={queryClient}>
    <Router />
  </QueryClientProvider>,
)

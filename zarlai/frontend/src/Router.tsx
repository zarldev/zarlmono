import { useState, useEffect } from 'react'
import AdminV2 from './admin-v2/AdminV2'
import Immersive from './Immersive'
import Onboard from './Onboard'

export default function Router() {
  const [path, setPath] = useState(window.location.pathname)

  useEffect(() => {
    function onPopState() { setPath(window.location.pathname) }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  function navigate(to: string) {
    window.history.pushState({}, '', to)
    setPath(to)
  }

  if (path === '/onboard') return <Onboard onNavigate={navigate} />
  if (path === '/admin') return <AdminV2 onNavigate={navigate} />
  return <Immersive onNavigate={navigate} />
}

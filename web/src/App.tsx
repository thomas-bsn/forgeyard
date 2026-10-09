import { useEffect, useState } from 'react'
import { api, ApiError, errorMessage, type Instance, type User } from './api'
import Setup from './Setup'
import Login from './Login'
import Dashboard from './Dashboard'

type State =
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'setup'; instance: Instance }
  | { kind: 'login'; instance: Instance }
  | { kind: 'ready'; instance: Instance; user: User }

/** The result of a Discord sign-in, passed back by the server in ?discord=… and removed from the URL. */
function takeDiscordStatus(): string | null {
  const params = new URLSearchParams(window.location.search)
  const status = params.get('discord')
  if (status) {
    params.delete('discord')
    const query = params.toString()
    window.history.replaceState(null, '', window.location.pathname + (query ? `?${query}` : ''))
  }
  return status
}

export default function App() {
  const [state, setState] = useState<State>({ kind: 'loading' })
  const [discordStatus] = useState(takeDiscordStatus)

  async function load() {
    try {
      const instance = await api.instance()
      document.title = instance.name
      if (instance.setupRequired) {
        setState({ kind: 'setup', instance })
        return
      }
      try {
        setState({ kind: 'ready', instance, user: await api.me() })
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) setState({ kind: 'login', instance })
        else throw err
      }
    } catch (err) {
      setState({ kind: 'error', message: errorMessage(err) })
    }
  }

  useEffect(() => {
    load()
  }, [])

  switch (state.kind) {
    case 'loading':
      return null
    case 'error':
      return (
        <div className="auth-screen">
          <div className="banner banner-down">
            <span className="dot dot-down" />
            Impossible de joindre le serveur : {state.message}
          </div>
        </div>
      )
    case 'setup':
      return <Setup instance={state.instance} onDone={load} />
    case 'login':
      return <Login instance={state.instance} discordStatus={discordStatus} onDone={load} />
    case 'ready':
      return <Dashboard instance={state.instance} user={state.user} onLogout={load} />
  }
}

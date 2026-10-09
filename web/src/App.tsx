import { useEffect, useState } from 'react'
import { api, ApiError, type Instance, type User } from './api'
import Setup from './Setup'
import Login from './Login'
import Dashboard from './Dashboard'

type State =
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'setup' }
  | { kind: 'login'; instance: Instance }
  | { kind: 'ready'; instance: Instance; user: User }

export default function App() {
  const [state, setState] = useState<State>({ kind: 'loading' })

  async function load() {
    try {
      const instance = await api.instance()
      document.title = instance.name
      if (instance.setupRequired) {
        setState({ kind: 'setup' })
        return
      }
      try {
        setState({ kind: 'ready', instance, user: await api.me() })
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) setState({ kind: 'login', instance })
        else throw err
      }
    } catch (err) {
      setState({ kind: 'error', message: err instanceof Error ? err.message : String(err) })
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
        <div className="center">
          <p className="error">Impossible de joindre le serveur : {state.message}</p>
        </div>
      )
    case 'setup':
      return <Setup onDone={load} />
    case 'login':
      return <Login instanceName={state.instance.name} onDone={load} />
    case 'ready':
      return <Dashboard instance={state.instance} user={state.user} onLogout={load} />
  }
}

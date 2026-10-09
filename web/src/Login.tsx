import { useState, type FormEvent } from 'react'
import { api } from './api'

export default function Login({ instanceName, onDone }: { instanceName: string; onDone: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await api.login(username, password)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="center">
      <form className="card" onSubmit={submit}>
        <h1>{instanceName}</h1>
        <button type="button" className="discord" disabled title="Disponible dans une prochaine version">
          Se connecter avec Discord <span className="badge">bientôt</span>
        </button>
        <div className="separator">ou avec un identifiant</div>
        <label>
          Identifiant
          <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required autoFocus />
        </label>
        <label>
          Mot de passe
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>
        {error && <p className="error">{error}</p>}
        <div className="actions">
          <button type="submit" disabled={busy}>
            {busy ? 'Connexion…' : 'Se connecter'}
          </button>
        </div>
      </form>
    </div>
  )
}

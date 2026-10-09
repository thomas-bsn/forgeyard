import { useState, type FormEvent } from 'react'
import { api } from './api'

const steps = ['Token de setup', 'Compte admin', 'Votre instance'] as const

export default function Setup({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState(0)
  const [token, setToken] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [instanceName, setInstanceName] = useState('Forgeyard')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  function next(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (step === 1) {
      if (password.length < 12) return setError('Le mot de passe doit faire au moins 12 caractères.')
      if (password !== confirm) return setError('Les mots de passe ne correspondent pas.')
    }
    setStep(step + 1)
  }

  async function finish(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await api.setup({ token, instanceName, username, password })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      // A wrong token is only detected at the end: send the user back to the step that needs fixing.
      if (err instanceof Error && err.message.includes('token')) setStep(0)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="center">
      <form className="card" onSubmit={step === steps.length - 1 ? finish : next}>
        <h1>Bienvenue sur Forgeyard</h1>
        <ol className="steps">
          {steps.map((label, i) => (
            <li key={label} className={i === step ? 'active' : i < step ? 'done' : ''}>
              {label}
            </li>
          ))}
        </ol>

        {step === 0 && (
          <>
            <p className="muted">
              Pour prouver que vous êtes bien l'installateur, collez le token affiché dans les logs du
              serveur (<code>docker logs forgeyard</code>).
            </p>
            <label>
              Token de setup
              <input value={token} onChange={(e) => setToken(e.target.value)} required autoFocus />
            </label>
          </>
        )}

        {step === 1 && (
          <>
            <p className="muted">Ce compte sera le superadmin de l'instance.</p>
            <div className="methods">
              <button type="button" className="method selected">
                Identifiant et mot de passe
              </button>
              <button type="button" className="method" disabled title="Disponible dans une prochaine version">
                Discord <span className="badge">bientôt</span>
              </button>
            </div>
            <label>
              Identifiant
              <input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                pattern="[a-zA-Z0-9_\-]{3,32}"
                title="3 à 32 caractères : lettres, chiffres, _ et -"
                autoComplete="username"
                required
                autoFocus
              />
            </label>
            <label>
              Mot de passe <span className="muted">(12 caractères minimum)</span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                required
              />
            </label>
            <label>
              Confirmer le mot de passe
              <input
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
                required
              />
            </label>
          </>
        )}

        {step === 2 && (
          <label>
            Nom de votre PaaS
            <input
              value={instanceName}
              onChange={(e) => setInstanceName(e.target.value)}
              maxLength={64}
              required
              autoFocus
            />
          </label>
        )}

        {error && <p className="error">{error}</p>}

        <div className="actions">
          {step > 0 && (
            <button type="button" className="secondary" onClick={() => setStep(step - 1)} disabled={busy}>
              Retour
            </button>
          )}
          <button type="submit" disabled={busy}>
            {step === steps.length - 1 ? (busy ? 'Installation…' : 'Terminer') : 'Continuer'}
          </button>
        </div>
      </form>
    </div>
  )
}

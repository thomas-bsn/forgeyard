import { useState, type FormEvent } from 'react'
import { api, ApiError, errorMessage, type Instance } from './api'
import { CopyField, DiscordIcon, Logo, ThemeToggle } from './ui'

const steps = ['Token', 'Méthode', 'Compte admin', 'Instance'] as const
type Method = 'discord' | 'password'

export default function Setup({ instance, onDone }: { instance: Instance; onDone: () => void }) {
  const [step, setStep] = useState(0)
  const [token, setToken] = useState('')
  const [method, setMethod] = useState<Method>('discord')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [instanceName, setInstanceName] = useState('Forgeyard')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const last = step === steps.length - 1

  function next(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (step === 2 && method === 'password') {
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
      if (method === 'discord') {
        const { authorizeUrl } = await api.setupDiscord({ token, instanceName, clientId, clientSecret })
        // Discord sends the browser back to the server, which makes this account the superadmin.
        window.location.href = authorizeUrl
        return
      }
      await api.setup({ token, instanceName, username, password })
      onDone()
    } catch (err) {
      setError(errorMessage(err))
      // A wrong token is only detected at the end: go back to the step that needs fixing.
      if (err instanceof ApiError && err.status === 403) setStep(0)
      setBusy(false)
    }
  }

  return (
    <div className="auth-screen">
      <div className="auth-column wide">
        <div className="brand">
          <Logo size={34} />
          Bienvenue sur Forgeyard
        </div>

        <ol className="stepper">
          {steps.map((label, i) => (
            <li key={label} className={i === step ? 'current' : i < step ? 'done' : ''}>
              <span className="step-num">{i < step ? '✓' : i + 1}</span>
              {label}
            </li>
          ))}
        </ol>

        <form className="panel" onSubmit={last ? finish : next}>
          {step === 0 && (
            <>
              <h2>Token de setup</h2>
              <p className="muted">
                Pour prouver que vous êtes bien l'installateur, collez le token affiché dans les logs du serveur (
                <code>docker logs forgeyard</code>).
              </p>
              <label className="field">
                <span>Token</span>
                <input value={token} onChange={(e) => setToken(e.target.value)} required autoFocus />
              </label>
            </>
          )}

          {step === 1 && (
            <>
              <h2>Comment allez-vous vous connecter ?</h2>
              <p className="muted">Ce compte sera le superadmin de l'instance. Il garde cette méthode de connexion.</p>
              <div className="auth-options">
                <button
                  type="button"
                  className={`auth-option ${method === 'discord' ? 'selected' : ''}`}
                  onClick={() => setMethod('discord')}
                  aria-pressed={method === 'discord'}
                >
                  <strong>Discord</strong>
                  <small>Vous et vos utilisateurs vous connectez avec votre compte Discord.</small>
                </button>
                <button
                  type="button"
                  className={`auth-option ${method === 'password' ? 'selected' : ''}`}
                  onClick={() => setMethod('password')}
                  aria-pressed={method === 'password'}
                >
                  <strong>Identifiant et mot de passe</strong>
                  <small>Discord pourra être activé plus tard pour les autres utilisateurs.</small>
                </button>
              </div>
            </>
          )}

          {step === 2 && method === 'password' && (
            <>
              <h2>Compte admin</h2>
              <label className="field">
                <span>Identifiant</span>
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
              <label className="field">
                <span>Mot de passe</span>
                <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required />
                <small>12 caractères minimum.</small>
              </label>
              <label className="field">
                <span>Confirmer le mot de passe</span>
                <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
              </label>
            </>
          )}

          {step === 2 && method === 'discord' && (
            <>
              <h2>Application Discord</h2>
              <p className="muted">Discord doit connaître votre PaaS avant de pouvoir y connecter quelqu'un. Ça prend 2 minutes, une seule fois.</p>
              <ol className="steps-help">
                <li>
                  Ouvrez le{' '}
                  <a href="https://discord.com/developers/applications" target="_blank" rel="noreferrer">
                    portail développeur Discord
                  </a>{' '}
                  et cliquez sur <b>New Application</b>.
                </li>
                <li>
                  Dans l'onglet <b>OAuth2</b>, ajoutez cette adresse dans <b>Redirects</b> :
                  <CopyField value={instance.discordRedirectUrl} />
                </li>
                <li>
                  Copiez le <b>Client ID</b> et le <b>Client Secret</b> ci-dessous.
                </li>
              </ol>
              <label className="field">
                <span>Client ID</span>
                <input value={clientId} onChange={(e) => setClientId(e.target.value)} inputMode="numeric" required autoFocus />
              </label>
              <label className="field">
                <span>Client Secret</span>
                <input type="password" value={clientSecret} onChange={(e) => setClientSecret(e.target.value)} autoComplete="off" required />
              </label>
            </>
          )}

          {step === 3 && (
            <>
              <h2>Votre instance</h2>
              <label className="field">
                <span>Nom de votre PaaS</span>
                <input value={instanceName} onChange={(e) => setInstanceName(e.target.value)} maxLength={64} required autoFocus />
              </label>
              {method === 'discord' && (
                <p className="muted">
                  En terminant, vous serez envoyé sur Discord. Le compte avec lequel vous vous connectez deviendra le superadmin.
                </p>
              )}
            </>
          )}

          {error && <p className="error">{error}</p>}

          <div className="panel-footer">
            <ThemeToggle />
            <span className="spacer" />
            {step > 0 && (
              <button type="button" className="btn" onClick={() => setStep(step - 1)} disabled={busy}>
                Retour
              </button>
            )}
            {last && method === 'discord' ? (
              <button type="submit" className="btn btn-discord" disabled={busy}>
                <DiscordIcon />
                {busy ? 'Redirection…' : 'Terminer avec Discord'}
              </button>
            ) : (
              <button type="submit" className="btn btn-primary" disabled={busy}>
                {last ? (busy ? 'Installation…' : 'Terminer') : 'Continuer'}
              </button>
            )}
          </div>
        </form>
      </div>
    </div>
  )
}

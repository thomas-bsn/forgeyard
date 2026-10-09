import { useState, type FormEvent } from 'react'
import { api, errorMessage, type Instance } from './api'
import { DiscordIcon, Logo, ThemeToggle } from './ui'

const authMessages: Record<string, { tone: 'warn' | 'down'; title: string; text: string }> = {
  pending: {
    tone: 'warn',
    title: 'Demande en attente',
    text: 'Un admin doit valider votre compte. Revenez vous connecter plus tard.',
  },
  refused: { tone: 'down', title: 'Demande refusée', text: "Un admin a refusé l'accès à ce compte Discord." },
  disabled: { tone: 'down', title: 'Compte désactivé', text: 'Contactez un admin de cette instance.' },
  cancelled: { tone: 'warn', title: 'Connexion annulée', text: "Vous avez refusé l'autorisation sur Discord." },
  expired: { tone: 'warn', title: 'Connexion expirée', text: 'Recommencez la connexion avec Discord.' },
  unavailable: { tone: 'warn', title: 'Discord indisponible', text: "La connexion Discord n'est pas configurée sur cette instance." },
  error: { tone: 'down', title: 'Échec de la connexion Discord', text: 'Réessayez. Si ça persiste, prévenez un admin.' },
  'link-invalid': {
    tone: 'down',
    title: 'Lien de connexion invalide',
    text: "Il a expiré ou a déjà servi. Relancez la commande admin-login sur le serveur.",
  },
}

export default function Login({
  instance,
  authStatus,
  onDone,
}: {
  instance: Instance
  authStatus: string | null
  onDone: () => void
}) {
  const passwordLogin = instance.passwordLoginEnabled || !instance.discordEnabled
  const [showPassword, setShowPassword] = useState(!instance.discordEnabled)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const message = authStatus ? authMessages[authStatus] : undefined

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await api.login(username, password)
      onDone()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <div className="auth-screen">
      <div className="auth-column">
        <div className="brand">
          <Logo size={34} />
          {instance.name}
        </div>

        <form className="panel" onSubmit={submit}>
          <h2>Connexion</h2>
          {instance.discordEnabled && (
            <>
              <p className="muted">Connectez-vous avec votre compte Discord pour accéder à vos apps.</p>
              <a className="btn btn-discord" href="/api/auth/discord">
                <DiscordIcon />
                Se connecter avec Discord
              </a>
            </>
          )}

          {!passwordLogin ? null : showPassword ? (
            <>
              {instance.discordEnabled && <div className="separator">ou avec un identifiant</div>}
              <label className="field">
                <span>Identifiant</span>
                <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required autoFocus={!instance.discordEnabled} />
              </label>
              <label className="field">
                <span>Mot de passe</span>
                <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
              </label>
              {error && <p className="error">{error}</p>}
              <button type="submit" className="btn btn-primary" disabled={busy}>
                {busy ? 'Connexion…' : 'Se connecter'}
              </button>
            </>
          ) : (
            <button type="button" className="btn btn-ghost" onClick={() => setShowPassword(true)}>
              Connexion avec un identifiant
            </button>
          )}

          <div className="panel-footer">
            <span className="spacer" />
            <ThemeToggle />
          </div>
        </form>

        {message && (
          <div className={`banner banner-${message.tone}`} role="status">
            <span className={`dot dot-${message.tone}`} />
            <span>
              <b>{message.title}</b> · {message.text}
            </span>
          </div>
        )}
      </div>
    </div>
  )
}

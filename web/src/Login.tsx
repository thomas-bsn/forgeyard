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
    <div className="login-screen">
      <div className="login-column">
        <svg className="login-art" viewBox="0 0 320 186" role="img" aria-label="Un serveur avec ses apps">
          <polygon points="160,60 280,120 160,180 40,120" className="login-art-platform" />
          <polygon points="160,45 188,59 160,73 132,59" fill="#a5b4fc" />
          <polygon points="132,59 160,73 160,109 132,95" fill="#818cf8" />
          <polygon points="160,73 188,59 188,95 160,109" fill="#6366f1" />
          <polygon points="105,72 133,86 105,100 77,86" fill="#a5b4fc" />
          <polygon points="77,86 105,100 105,136 77,122" fill="#818cf8" />
          <polygon points="105,100 133,86 133,122 105,136" fill="#6366f1" />
          <polygon points="215,72 243,86 215,100 187,86" fill="#86efac" />
          <polygon points="187,86 215,100 215,136 187,122" fill="#22c55e" />
          <polygon points="215,100 243,86 243,122 215,136" fill="#15803d" />
        </svg>

        <form className="login-card" onSubmit={submit}>
          <div className="login-brand">
            <div className="login-name">
              <Logo size={30} />
              {instance.name}
            </div>
            <p className="muted">Vos apps, sur vos machines.</p>
          </div>

          {message && (
            <div className={`banner banner-${message.tone}`} role="status">
              <span className={`dot dot-${message.tone}`} />
              <span>
                <b>{message.title}</b> · {message.text}
              </span>
            </div>
          )}

          {instance.discordEnabled && (
            <a className="btn btn-discord btn-block login-discord" href="/api/auth/discord">
              <DiscordIcon />
              Continuer avec Discord
            </a>
          )}

          {!passwordLogin ? null : showPassword ? (
            <>
              {instance.discordEnabled && <div className="separator">ou avec un identifiant</div>}
              <label className="field">
                <span>Identifiant</span>
                <input value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="username" required autoFocus />
              </label>
              <label className="field">
                <span>Mot de passe</span>
                <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
              </label>
              {error && <p className="error">{error}</p>}
              <button type="submit" className="btn btn-primary btn-block" disabled={busy}>
                {busy ? 'Connexion…' : 'Se connecter'}
              </button>
            </>
          ) : (
            <button type="button" className="btn btn-block login-secondary" onClick={() => setShowPassword(true)}>
              Utiliser un identifiant
            </button>
          )}

          {instance.discordEnabled && <p className="login-hint">Nouveau ? Connectez-vous avec Discord : un admin validera votre accès.</p>}
        </form>

        <div className="login-footer">
          <span>Propulsé par Forgeyard · open source</span>
          <ThemeToggle />
        </div>
      </div>
    </div>
  )
}

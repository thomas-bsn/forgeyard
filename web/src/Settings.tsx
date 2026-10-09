import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type DiscordSettings } from './api'
import { DiscordAppSteps } from './ui'
import DomainPanel from './Domain'

function DiscordSettingsPanel() {
  const [settings, setSettings] = useState<DiscordSettings | null>(null)
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api
      .discordSettings()
      .then((s) => {
        setSettings(s)
        setClientId(s.clientId)
      })
      .catch((err) => setError(errorMessage(err)))
  }, [])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setSaved(false)
    try {
      const s = await api.saveDiscordSettings(clientId, clientSecret)
      setSettings(s)
      setClientSecret('')
      setSaved(true)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!settings) return error ? <p className="error">{error}</p> : null
  const enabled = settings.clientId !== '' && settings.hasSecret

  return (
    <form className="panel" onSubmit={save}>
      <div className="header">
        <h2 className="header-title">Connexion Discord</h2>
        <span className={`banner ${enabled ? 'banner-up' : ''}`}>
          <span className={`dot ${enabled ? 'dot-up' : ''}`} />
          {enabled ? 'Activée' : 'Désactivée'}
        </span>
      </div>
      <DiscordAppSteps redirectUrl={settings.redirectUrl} />
      <label className="field">
        <span>Client ID</span>
        <input value={clientId} onChange={(e) => setClientId(e.target.value)} inputMode="numeric" placeholder="123456789012345678" />
        <small>Laissez vide pour désactiver la connexion Discord.</small>
      </label>
      <label className="field">
        <span>Client Secret</span>
        <input
          type="password"
          value={clientSecret}
          onChange={(e) => setClientSecret(e.target.value)}
          placeholder={settings.hasSecret ? '•••••••• (inchangé)' : ''}
          autoComplete="off"
        />
        <small>Chiffré avant d'être enregistré, jamais réaffiché. Laissez vide pour garder l'actuel.</small>
      </label>
      {error && <p className="error">{error}</p>}
      <div className="panel-footer">
        {saved && <span className="muted">Enregistré.</span>}
        <span className="spacer" />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Enregistrement…' : 'Enregistrer'}
        </button>
      </div>
    </form>
  )
}

export default function Settings({ superadmin }: { superadmin: boolean }) {
  return (
    <div className="section">
      {superadmin && <DomainPanel />}
      {superadmin && <LoginSettingsPanel />}
      <DiscordSettingsPanel />
    </div>
  )
}

/** Superadmin only: whether people may sign in with a username and password. */
function LoginSettingsPanel() {
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api
      .loginSettings()
      .then((s) => setEnabled(s.passwordLogin))
      .catch((err) => setError(errorMessage(err)))
  }, [])

  async function toggle() {
    if (enabled === null) return
    setBusy(true)
    setError('')
    try {
      setEnabled((await api.saveLoginSettings(!enabled)).passwordLogin)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (enabled === null) return error ? <p className="error">{error}</p> : null
  return (
    <div className="panel">
      <div className="header">
        <div className="header-title">
          <h2>Connexion par identifiant</h2>
          <p className="muted">Permet de se connecter avec un identifiant et un mot de passe, en plus de Discord.</p>
        </div>
        <span className={`banner ${enabled ? 'banner-up' : ''}`}>
          <span className={`dot ${enabled ? 'dot-up' : ''}`} />
          {enabled ? 'Activée' : 'Désactivée'}
        </span>
      </div>
      {error && <p className="error">{error}</p>}
      <div className="panel-footer">
        <span className="spacer" />
        <button type="button" className="btn" onClick={toggle} disabled={busy}>
          {enabled ? 'Désactiver' : 'Activer'}
        </button>
      </div>
    </div>
  )
}

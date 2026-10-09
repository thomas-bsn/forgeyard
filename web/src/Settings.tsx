import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type DiscordSettings } from './api'
import { CopyField } from './ui'

export default function Settings() {
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
      <p className="muted">
        Créez une application sur le{' '}
        <a href="https://discord.com/developers/applications" target="_blank" rel="noreferrer">
          portail développeur Discord
        </a>{' '}
        et ajoutez cette adresse dans <b>OAuth2 › Redirects</b> :
      </p>
      <CopyField value={settings.redirectUrl} />
      <label className="field">
        <span>Client ID</span>
        <input value={clientId} onChange={(e) => setClientId(e.target.value)} inputMode="numeric" />
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

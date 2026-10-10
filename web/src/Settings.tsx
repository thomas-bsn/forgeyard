import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type DiscordSettings } from './api'
import { CopyField, DiscordAppSteps } from './ui'
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
        <h2 className="header-title">Application Discord</h2>
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

type Section = 'general' | 'domain' | 'login' | 'discord'

const sections: { id: Section; label: string; superadmin: boolean }[] = [
  { id: 'general', label: 'Général', superadmin: true },
  { id: 'domain', label: 'Domaine des apps', superadmin: true },
  { id: 'login', label: 'Connexion', superadmin: true },
  { id: 'discord', label: 'Discord', superadmin: false },
]

const intros: Record<Section, string> = {
  general: 'Le nom de votre PaaS et l’adresse à laquelle on ouvre Forgeyard.',
  domain: 'Chaque app reçoit une adresse sous ce domaine, et Forgeyard peut créer son DNS.',
  login: 'Comment les comptes se connectent.',
  discord: 'L’application Discord qui permet de se connecter avec Discord.',
}

export default function Settings({ superadmin, section, onRenamed }: { superadmin: boolean; section?: string; onRenamed: () => void }) {
  const available = sections.filter((x) => superadmin || !x.superadmin)
  const current = available.find((x) => x.id === section) ?? available[0]

  return (
    <div className="settings">
      <nav className="settings-nav" aria-label="Réglages">
        {available.map((x) => (
          <a key={x.id} href={`#/settings/${x.id}`} aria-current={x.id === current.id ? 'page' : undefined}>
            {x.label}
          </a>
        ))}
      </nav>
      <div className="settings-main">
        <div>
          <h1>{current.label}</h1>
          <p className="muted">{intros[current.id]}</p>
        </div>
        {current.id === 'general' && <GeneralPanel onRenamed={onRenamed} />}
        {current.id === 'domain' && <DomainPanel />}
        {current.id === 'login' && <LoginSettingsPanel />}
        {current.id === 'discord' && <DiscordSettingsPanel />}
      </div>
    </div>
  )
}

/** Superadmin only: the instance's name and Forgeyard's own address. */
function GeneralPanel({ onRenamed }: { onRenamed: () => void }) {
  const [loaded, setLoaded] = useState(false)
  const [name, setName] = useState('')
  const [publicUrl, setPublicUrl] = useState('')
  const [savedUrl, setSavedUrl] = useState('')
  const [redirect, setRedirect] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    Promise.all([api.instance(), api.domainSettings()])
      .then(([i, d]) => {
        setName(i.name)
        setPublicUrl(d.publicUrl)
        setSavedUrl(d.publicUrl)
        setRedirect(d.discordRedirectUrl)
        setLoaded(true)
      })
      .catch((err) => setError(errorMessage(err)))
  }, [])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const s = await api.saveGeneralSettings(name, publicUrl)
      setName(s.name)
      setPublicUrl(s.publicUrl)
      if (s.publicUrl !== savedUrl) {
        setRedirect(s.publicUrl + '/api/auth/discord/callback')
        setNotice('Enregistré. L’adresse a changé : mettez à jour la redirection de votre application Discord (ci-dessous) et ouvrez désormais Forgeyard depuis la nouvelle adresse.')
      } else {
        setNotice('Enregistré.')
      }
      setSavedUrl(s.publicUrl)
      onRenamed()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!loaded) return error ? <p className="error">{error}</p> : null
  return (
    <form className="panel" onSubmit={save}>
      <label className="field">
        <span>Nom du PaaS</span>
        <input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} required />
      </label>
      <label className="field">
        <span>Adresse de Forgeyard</span>
        <input type="url" value={publicUrl} onChange={(e) => setPublicUrl(e.target.value)} placeholder="https://forgeyard.mondomaine.com" required />
        <small>Utilisée pour la connexion Discord, les commandes des nodes et le lien de secours.</small>
      </label>
      <div className="field">
        <span>Redirection Discord</span>
        <CopyField value={redirect} />
        <small>À déclarer à l’identique dans votre application Discord.</small>
      </div>
      {error && <p className="error">{error}</p>}
      {notice && <p className="muted">{notice}</p>}
      <div className="panel-footer">
        <span className="spacer" />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Enregistrement…' : 'Enregistrer'}
        </button>
      </div>
    </form>
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

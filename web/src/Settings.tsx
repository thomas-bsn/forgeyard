import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type DiscordSettings, type Instance } from './api'
import { CopyField, DiscordAppSteps, Switch } from './ui'
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
  const [layout, setLayout] = useState<Instance['appsLayout']>('sidebar')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    Promise.all([api.instance(), api.domainSettings()])
      .then(([i, d]) => {
        setName(i.name)
        setLayout(i.appsLayout)
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
      const s = await api.saveGeneralSettings(name, publicUrl, layout)
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
        <span>Affichage de l’onglet Apps</span>
        <div className="layout-choices">
          {(
            [
              ['sidebar', 'Nodes à gauche', 'La liste des nodes à gauche, les apps du node choisi en cartes détaillées.'],
              ['nodes', 'Cartes de nodes', 'Une carte par node en haut, ses apps en tuiles en dessous.'],
              ['launcher', 'Icônes', 'Des onglets de nodes et les apps en grandes icônes, comme un téléphone.'],
            ] as const
          ).map(([value, title, text]) => (
            <button key={value} type="button" className={`auth-option ${layout === value ? 'selected' : ''}`} aria-pressed={layout === value} onClick={() => setLayout(value)}>
              <strong>
                {title}
                {value === 'sidebar' && <span className="badge">par défaut</span>}
              </strong>
              <small>{text}</small>
            </button>
          ))}
        </div>
        <small>Pour tous les membres de l’instance.</small>
      </div>
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

  async function toggle(next: boolean) {
    setBusy(true)
    setError('')
    try {
      setEnabled((await api.saveLoginSettings(next)).passwordLogin)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (enabled === null) return error ? <p className="error">{error}</p> : null
  return (
    <div className="panel">
      <div className="setting-row">
        <div className="header-title">
          <h2>Identifiant et mot de passe</h2>
          <p className="muted">
            {enabled
              ? 'Activé : on peut se connecter avec un identifiant et un mot de passe, en plus de Discord.'
              : 'Désactivé : tout le monde se connecte avec Discord.'}
          </p>
        </div>
        <Switch checked={enabled} onChange={toggle} disabled={busy} label="Connexion par identifiant et mot de passe" />
      </div>
      {error && <p className="error">{error}</p>}
    </div>
  )
}

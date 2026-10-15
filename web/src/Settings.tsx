import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type BackupSettings, type DiscordSettings, type Instance, type NotifySettings } from './api'
import { CopyField, DiscordAppSteps, Switch, WebhookForm, WebhookSteps } from './ui'
import DomainPanel from './Domain'
import { IconPicker } from './Cropper'

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

type Section = 'general' | 'domain' | 'login' | 'discord' | 'notifications' | 'backups'

const sections: { id: Section; label: string; superadmin: boolean }[] = [
  { id: 'general', label: 'Général', superadmin: true },
  { id: 'domain', label: 'Domaine des apps', superadmin: true },
  { id: 'login', label: 'Connexion', superadmin: true },
  { id: 'discord', label: 'Discord', superadmin: false },
  { id: 'notifications', label: 'Notifications', superadmin: false },
  { id: 'backups', label: 'Sauvegardes', superadmin: false },
]

const intros: Record<Section, string> = {
  general: 'Le nom de votre PaaS et l’adresse à laquelle on ouvre Forgeyard.',
  domain: 'Chaque app reçoit une adresse sous ce domaine, et Forgeyard peut créer son DNS.',
  login: 'Comment les comptes se connectent.',
  discord: 'L’application Discord qui permet de se connecter avec Discord.',
  notifications: 'Les alertes envoyées aux admins dans un salon Discord.',
  backups: 'Où vont les sauvegardes des volumes des stacks : un stockage compatible S3, hors des nodes, chiffré par restic.',
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
        {current.id === 'notifications' && <NotifyPanel />}
        {current.id === 'backups' && <BackupsPanel />}
      </div>
    </div>
  )
}

const notifyKinds: { key: keyof NotifySettings['events']; title: string; text: string }[] = [
  { key: 'requests', title: 'Demandes de compte', text: 'Quelqu’un se connecte avec Discord et attend votre validation.' },
  { key: 'support', title: 'Support', text: 'Une demande d’aide, ou un nouveau message d’un membre sur la sienne.' },
  { key: 'crashes', title: 'Apps suspendues', text: 'Une app plante 3 fois en 5 minutes : Forgeyard l’arrête.' },
  { key: 'nodes', title: 'Nodes hors ligne', text: 'Un node ne répond plus depuis une minute, puis quand il revient.' },
  { key: 'backups', title: 'Sauvegardes en échec', text: 'La sauvegarde planifiée d’un volume n’a pas abouti.' },
]

/** Admins: the Discord channel that receives the instance's alerts. */
function NotifyPanel() {
  const [settings, setSettings] = useState<NotifySettings | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.notifySettings().then(setSettings, (err) => setError(errorMessage(err)))
  }, [])

  if (!settings) return error ? <p className="error">{error}</p> : null

  async function setEvent(key: keyof NotifySettings['events'], value: boolean) {
    setError('')
    try {
      setSettings(await api.saveNotifySettings({ events: { ...settings!.events, [key]: value } }))
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <>
      <section className="panel">
        <h2>Salon des admins</h2>
        <WebhookSteps />
        <WebhookForm
          isSet={settings.webhookSet}
          onSave={async (webhook) => setSettings(await api.saveNotifySettings({ webhook, events: settings.events }))}
          onTest={() => api.testNotify()}
          onClear={async () => setSettings(await api.saveNotifySettings({ clear: true, events: settings.events }))}
        />
      </section>
      <section className="panel">
        <h2>Salon support <span className="muted">(facultatif)</span></h2>
        <p className="muted">
          Les demandes d’aide et de compte arrivent ici, à part des alertes. Sans ce salon, elles vont dans celui des admins.
        </p>
        <WebhookForm
          isSet={settings.supportWebhookSet}
          onSave={async (webhook) => setSettings(await api.saveNotifySettings({ supportWebhook: webhook, events: settings.events }))}
          onTest={() => api.testSupportNotify()}
          onClear={async () => setSettings(await api.saveNotifySettings({ clearSupport: true, events: settings.events }))}
        />
      </section>
      <section className="panel">
        <h2>Ce qui est envoyé</h2>
        {notifyKinds.map((k) => (
          <div key={k.key} className="setting-row">
            <div className="header-title">
              <strong>{k.title}</strong>
              <p className="muted">{k.text}</p>
            </div>
            <Switch checked={settings.events[k.key]} onChange={(v) => setEvent(k.key, v)} label={k.title} />
          </div>
        ))}
        <p className="muted">Chaque membre peut aussi recevoir les alertes de ses propres apps : Mon profil › Notifications.</p>
        {error && <p className="error">{error}</p>}
      </section>
    </>
  )
}

/** Admins: the S3-compatible storage the volumes' backups go to, and the password that encrypts them. */
function BackupsPanel() {
  const [settings, setSettings] = useState<BackupSettings | null>(null)
  const [form, setForm] = useState({ endpoint: '', bucket: '', prefix: 'forgeyard', region: '', accessKey: '', secretKey: '', password: '' })
  const [showPassword, setShowPassword] = useState(false)
  const [test, setTest] = useState<{ ok: boolean; error?: string; log?: string } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  function loaded(s: BackupSettings) {
    setSettings(s)
    setForm({ endpoint: s.endpoint, bucket: s.bucket, prefix: s.prefix || (s.configured ? '' : 'forgeyard'), region: s.region, accessKey: s.accessKey, secretKey: '', password: s.password ?? '' })
  }
  useEffect(() => {
    api.backupSettings().then(loaded, (err) => setError(errorMessage(err)))
  }, [])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setTest(null)
    try {
      loaded(await api.saveBackupSettings(form))
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function runTest() {
    setBusy(true)
    setTest(null)
    setError('')
    try {
      setTest(await api.testBackupSettings())
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!settings) return error ? <p className="error">{error}</p> : null
  const field = (key: keyof typeof form, label: string, extra: { placeholder?: string; type?: string; required?: boolean; help?: string } = {}) => (
    <label className="field">
      <span>{label}</span>
      <input
        type={extra.type ?? 'text'}
        value={form[key]}
        onChange={(e) => setForm({ ...form, [key]: e.target.value })}
        placeholder={extra.placeholder}
        required={extra.required}
        autoComplete="off"
      />
      {extra.help && <small>{extra.help}</small>}
    </label>
  )
  return (
    <>
      <form className="panel" onSubmit={save}>
        <h2>Stockage</h2>
        <p className="muted">
          Un bucket chez un fournisseur compatible S3 (Scaleway, OVHcloud, Backblaze B2, AWS, Cloudflare R2, un MinIO ailleurs…). Les
          sauvegardes ne restent jamais sur les nodes : un disque qui lâche ne les emporte pas.
        </p>
        <div className="form-row">
          {field('endpoint', 'Adresse', { placeholder: 'https://s3.fr-par.scw.cloud', required: true })}
          {field('region', 'Région', { placeholder: 'fr-par (si demandée)' })}
        </div>
        <div className="form-row">
          {field('bucket', 'Bucket', { placeholder: 'mes-sauvegardes', required: true })}
          {field('prefix', 'Dossier', { placeholder: 'forgeyard' })}
        </div>
        <div className="form-row">
          {field('accessKey', 'Clé d’accès', { required: true })}
          {field('secretKey', 'Clé secrète', { type: 'password', required: !settings.secretSet, placeholder: settings.secretSet ? 'enregistrée : laisser vide pour la garder' : '' })}
        </div>
        <label className="field">
          <span>Mot de passe du dépôt</span>
          <span className="password-row">
            <input
              type={showPassword ? 'text' : 'password'}
              className="mono"
              value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
              placeholder="créé à l’enregistrement"
              autoComplete="off"
            />
            <button type="button" className="btn btn-small" onClick={() => setShowPassword(!showPassword)}>
              {showPassword ? 'Masquer' : 'Afficher'}
            </button>
          </span>
          <small>
            restic chiffre les sauvegardes avec ce mot de passe. Gardez-en une copie hors de Forgeyard : sans lui, elles sont illisibles. Le
            changer ne rend pas lisibles celles déjà faites avec l’ancien.
          </small>
        </label>
        {error && <p className="error">{error}</p>}
        <div className="panel-footer">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Enregistrer
          </button>
          <button type="button" className="btn" disabled={busy || !settings.configured} onClick={runTest}>
            Tester
          </button>
        </div>
        {test &&
          (test.ok ? (
            <p className="state-up">Le dépôt répond : les sauvegardes peuvent y aller.</p>
          ) : (
            <div className="banner banner-down">
              <span className="dot dot-down" />
              <span>
                {test.error}
                {test.log && <pre className="mono test-log">{test.log}</pre>}
              </span>
            </div>
          ))}
      </form>
      <section className="panel">
        <h2>Ce qui est sauvegardé</h2>
        <p className="muted">
          Les volumes nommés des stacks, volume par volume, depuis la page de la stack (onglet Volumes et sauvegardes) : tous les jours ou toutes
          les heures, en gardant les N dernières. Un volume utilisé par un service marqué <span className="mono">x-forgeyard: {'{ backup: { type: postgres } }'}</span>{' '}
          est sauvegardé avec pg_dump, une copie cohérente de la base. Une restauration va toujours dans un volume neuf.
        </p>
      </section>
    </>
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
  const [icon, setIcon] = useState<string | undefined>()
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    Promise.all([api.instance(), api.domainSettings()])
      .then(([i, d]) => {
        setName(i.name)
        setLayout(i.appsLayout)
        setIcon(i.iconUrl)
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
      <div className="field">
        <span>Icône</span>
        <IconPicker
          current={icon}
          busy={busy}
          onPick={async (dataUrl) => {
            setError('')
            try {
              if (dataUrl) setIcon((await api.uploadIcon(dataUrl)).iconUrl)
              else {
                await api.deleteIcon()
                setIcon(undefined)
              }
              onRenamed()
            } catch (err) {
              setError(errorMessage(err))
            }
          }}
        />
      </div>
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

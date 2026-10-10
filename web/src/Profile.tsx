import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, errorMessage, type Session, type User } from './api'
import { Avatar } from './ui'

type Section = 'profile' | 'security' | 'notifications' | 'ssh' | 'tokens'

const sections: { id: Section; label: string; soon?: boolean }[] = [
  { id: 'profile', label: 'Profil' },
  { id: 'security', label: 'Sécurité' },
  { id: 'notifications', label: 'Notifications', soon: true },
  { id: 'ssh', label: 'Clés SSH', soon: true },
  { id: 'tokens', label: 'Jetons d’API', soon: true },
]

const intros: Record<Section, string> = {
  profile: 'Ce que les autres voient de vous dans Forgeyard.',
  security: 'Votre mot de passe et les appareils connectés à votre compte.',
  notifications: 'Être prévenu quand une de vos apps a un problème.',
  ssh: 'Ouvrir un terminal dans vos conteneurs avec vos clés SSH.',
  tokens: 'Piloter Forgeyard depuis un script, une CI ou un outil en ligne de commande.',
}

/** The signed-in user's own settings. */
export default function Profile({ user, section, onChange }: { user: User; section?: string; onChange: () => void }) {
  const current = sections.find((x) => x.id === section) ?? sections[0]
  return (
    <div className="settings">
      <nav className="settings-nav" aria-label="Mon compte">
        {sections.map((x) => (
          <a key={x.id} href={`#/profile/${x.id}`} aria-current={x.id === current.id ? 'page' : undefined}>
            {x.label}
            {x.soon && <span className="badge">bientôt</span>}
          </a>
        ))}
      </nav>
      <div className="settings-main">
        <div>
          <h1>{current.label}</h1>
          <p className="muted">{intros[current.id]}</p>
        </div>
        {current.id === 'profile' && <ProfilePanel user={user} onChange={onChange} />}
        {current.id === 'security' && <SecurityPanel user={user} />}
        {current.id === 'notifications' && (
          <ComingSoon
            items={[
              'Un email ou un message Discord quand une de vos apps plante, redémarre en boucle ou est suspendue.',
              'Le choix de ce qui vous prévient, app par app.',
            ]}
          />
        )}
        {current.id === 'ssh' && (
          <ComingSoon
            items={[
              'Ajouter vos clés SSH publiques (ed25519, RSA).',
              'Ouvrir un shell dans vos conteneurs : ssh monapp@forgeyard.mondomaine.com, sans serveur SSH dans le conteneur.',
              'Le même terminal directement dans le navigateur.',
            ]}
          />
        )}
        {current.id === 'tokens' && (
          <ComingSoon
            items={[
              'Créer des jetons limités à certaines apps et actions (déployer, redémarrer, lire les logs).',
              'Redéployer depuis GitHub Actions après chaque push.',
              'Une date d’expiration et la dernière utilisation de chaque jeton.',
            ]}
          />
        )}
      </div>
    </div>
  )
}

function ComingSoon({ items }: { items: string[] }) {
  return (
    <section className="panel coming-soon">
      <span className="badge badge-accent">Bientôt</span>
      <p>Ce qui arrive ici :</p>
      <ul>
        {items.map((i) => (
          <li key={i}>{i}</li>
        ))}
      </ul>
    </section>
  )
}

/** Resizes a picture to a 256×256 square, cropped at its centre, so uploads stay small. */
function resizePicture(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file)
    const img = new Image()
    img.onload = () => {
      const side = Math.min(img.width, img.height)
      const canvas = document.createElement('canvas')
      canvas.width = canvas.height = 256
      canvas.getContext('2d')!.drawImage(img, (img.width - side) / 2, (img.height - side) / 2, side, side, 0, 0, 256, 256)
      URL.revokeObjectURL(url)
      const webp = canvas.toDataURL('image/webp', 0.85)
      // Browsers that cannot encode WebP give back a PNG: JPEG is lighter then.
      resolve(webp.startsWith('data:image/webp') ? webp : canvas.toDataURL('image/jpeg', 0.85))
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('Image illisible.'))
    }
    img.src = url
  })
}

function ProfilePanel({ user, onChange }: { user: User; onChange: () => void }) {
  const discord = user.method === 'discord'
  const [followDiscord, setFollowDiscord] = useState(user.nameFromDiscord)
  const [name, setName] = useState(user.displayName)
  const [bio, setBio] = useState(user.bio)
  const [email, setEmail] = useState(user.email)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const file = useRef<HTMLInputElement>(null)

  async function run(action: () => Promise<unknown>, done: string) {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await action()
      setNotice(done)
      onChange()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  function save(e: FormEvent) {
    e.preventDefault()
    run(() => api.saveProfile({ displayName: name, nameFromDiscord: followDiscord, bio, email }), 'Profil enregistré.')
  }

  return (
    <>
      <section className="panel">
        <h2>Photo de profil</h2>
        <div className="avatar-row">
          <Avatar url={user.avatarUrl} name={user.displayName} size={72} />
          <div className="avatar-actions">
            <p className="muted">
              {user.customAvatar
                ? 'Votre propre photo.'
                : discord && user.avatarUrl
                  ? 'Celle de votre compte Discord, mise à jour à chaque connexion.'
                  : discord
                    ? 'Forgeyard n’a pas encore votre photo Discord : elle arrive à la prochaine connexion avec Discord.'
                    : 'Votre initiale, tant que vous n’avez pas envoyé de photo.'}
            </p>
            <div className="row-actions">
              <button type="button" className="btn" disabled={busy} onClick={() => file.current?.click()}>
                Envoyer une photo
              </button>
              {discord && (
                <a className="btn" href="/api/auth/discord?return=profile" title="Reprend votre photo et votre nom Discord">
                  Mettre à jour depuis Discord
                </a>
              )}
              {user.customAvatar && (
                <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.deleteAvatar(), 'Photo retirée.')}>
                  {discord ? 'Reprendre celle de Discord' : 'Retirer la photo'}
                </button>
              )}
            </div>
            <input
              ref={file}
              type="file"
              accept="image/png,image/jpeg,image/webp,image/gif"
              hidden
              onChange={(e) => {
                const f = e.target.files?.[0]
                e.target.value = ''
                if (f) run(async () => api.uploadAvatar(await resizePicture(f)), 'Photo enregistrée.')
              }}
            />
          </div>
        </div>
      </section>

      <form className="panel" onSubmit={save}>
        <h2>Informations</h2>
        {user.username && (
          <label className="field">
            <span>Identifiant</span>
            <input value={user.username} disabled />
            <small>Il sert à vous connecter et ne change pas.</small>
          </label>
        )}
        {discord && (
          <label className="check">
            <input type="checkbox" checked={followDiscord} onChange={(e) => setFollowDiscord(e.target.checked)} />
            <span>
              <b>Utiliser mon nom Discord</b>
              <small>{user.discordName ? `« ${user.discordName} », mis à jour à chaque connexion.` : 'Mis à jour à chaque connexion.'}</small>
            </span>
          </label>
        )}
        <label className="field">
          <span>Nom affiché</span>
          <input value={followDiscord && user.discordName ? user.discordName : name} onChange={(e) => setName(e.target.value)} maxLength={64} required disabled={followDiscord} />
        </label>
        <label className="field">
          <span>Description</span>
          <textarea value={bio} onChange={(e) => setBio(e.target.value)} maxLength={280} rows={3} placeholder="Ce que vous faites, vos projets…" />
          <small>{bio.length} / 280</small>
        </label>
        <label className="field">
          <span>Email</span>
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="vous@exemple.com" />
          <small>Pour les notifications, plus tard. {discord ? 'Prérempli avec celui de Discord.' : ''}</small>
        </label>
        {error && <p className="error">{error}</p>}
        {notice && <p className="muted">{notice}</p>}
        <div className="panel-footer">
          <span className="muted">Membre depuis le {new Date(user.createdAt * 1000).toLocaleDateString('fr-FR')}</span>
          <span className="spacer" />
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Enregistrer
          </button>
        </div>
      </form>
    </>
  )
}

/** A readable name for a browser's user agent: "Firefox · macOS". */
function deviceName(ua: string): string {
  if (!ua) return 'Appareil inconnu'
  const browser = /Edg\//.test(ua)
    ? 'Edge'
    : /Firefox\//.test(ua)
      ? 'Firefox'
      : /Chrome\//.test(ua)
        ? 'Chrome'
        : /Safari\//.test(ua)
          ? 'Safari'
          : /curl\//.test(ua)
            ? 'curl'
            : 'Navigateur'
  const os = /iPhone|iPad/.test(ua)
    ? 'iOS'
    : /Android/.test(ua)
      ? 'Android'
      : /Mac OS X/.test(ua)
        ? 'macOS'
        : /Windows/.test(ua)
          ? 'Windows'
          : /Linux/.test(ua)
            ? 'Linux'
            : ''
  return os ? `${browser} · ${os}` : browser
}

function SecurityPanel({ user }: { user: User }) {
  const [sessions, setSessions] = useState<Session[] | null>(null)
  const [error, setError] = useState('')

  async function load() {
    try {
      setSessions(await api.sessions())
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  useEffect(() => {
    load()
  }, [])

  async function signOut(id: string) {
    setError('')
    try {
      await api.deleteSession(id)
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const others = sessions?.filter((s) => !s.current).length ?? 0

  return (
    <>
      {user.method === 'password' ? (
        <PasswordPanel />
      ) : (
        <section className="panel">
          <h2>Mot de passe</h2>
          <p className="muted">Vous vous connectez avec Discord : votre mot de passe et votre double authentification se gèrent sur Discord.</p>
        </section>
      )}

      <section className="panel">
        <div className="setting-row">
          <div className="header-title">
            <h2>Appareils connectés</h2>
            <p className="muted">Chaque connexion reste valable 30 jours.</p>
          </div>
          {others > 0 && (
            <button type="button" className="btn" onClick={() => signOut('others')}>
              Déconnecter les autres
            </button>
          )}
        </div>
        {error && <p className="error">{error}</p>}
        {sessions && (
          <div className="mini-list">
            {sessions.map((s) => (
              <div key={s.id} className="mini-list-row">
                <span className={`dot ${s.current ? 'dot-up' : ''}`} />
                <span className="session-main">
                  <strong>{deviceName(s.userAgent)}</strong>
                  <span className="muted">
                    {s.ip || 'IP inconnue'} · connecté le {new Date(s.createdAt * 1000).toLocaleString('fr-FR', { dateStyle: 'short', timeStyle: 'short' })}
                  </span>
                </span>
                {s.current ? (
                  <span className="badge badge-accent">cet appareil</span>
                ) : (
                  <button type="button" className="btn btn-small" onClick={() => signOut(s.id)}>
                    Déconnecter
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </section>
    </>
  )
}

function PasswordPanel() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  async function save(e: FormEvent) {
    e.preventDefault()
    setError('')
    setNotice('')
    if (next.length < 12) return setError('Le nouveau mot de passe doit faire au moins 12 caractères.')
    if (next !== confirm) return setError('Les deux nouveaux mots de passe ne correspondent pas.')
    setBusy(true)
    try {
      await api.changePassword(current, next)
      setCurrent('')
      setNext('')
      setConfirm('')
      setNotice('Mot de passe changé. Vos autres appareils ont été déconnectés.')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel" onSubmit={save}>
      <h2>Mot de passe</h2>
      <label className="field">
        <span>Mot de passe actuel</span>
        <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" required />
      </label>
      <div className="form-row">
        <label className="field">
          <span>Nouveau mot de passe</span>
          <input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" required />
          <small>12 caractères minimum.</small>
        </label>
        <label className="field">
          <span>Confirmer</span>
          <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
        </label>
      </div>
      {error && <p className="error">{error}</p>}
      {notice && <p className="muted">{notice}</p>}
      <div className="panel-footer">
        <span className="spacer" />
        <button type="submit" className="btn btn-primary" disabled={busy}>
          Changer le mot de passe
        </button>
      </div>
    </form>
  )
}

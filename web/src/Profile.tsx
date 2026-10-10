import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, errorMessage, type Session, type SSHKey, type User } from './api'
import { Avatar, CopyField, since, Switch, WebhookForm, WebhookSteps } from './ui'
import { ImageCropper } from './Cropper'

type Section = 'profile' | 'security' | 'notifications' | 'ssh' | 'tokens'

const sections: { id: Section; label: string; soon?: boolean }[] = [
  { id: 'profile', label: 'Profil' },
  { id: 'security', label: 'Sécurité' },
  { id: 'notifications', label: 'Notifications' },
  { id: 'ssh', label: 'Clés SSH' },
  { id: 'tokens', label: 'Jetons d’API', soon: true },
]

const intros: Record<Section, string> = {
  profile: 'Ce que les autres voient de vous dans Forgeyard.',
  security: 'Votre mot de passe et les appareils connectés à votre compte.',
  notifications: 'Être prévenu quand une de vos apps a un problème.',
  ssh: 'Se connecter en SSH à vos apps et à vos sandboxes : ssh monapp@… -p 2222.',
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
          <section className="panel">
            <h2>Alertes de mes apps sur Discord</h2>
            <p className="muted">
              Vous recevez un message quand une de vos apps plante (au plus un toutes les 10 minutes par app) et quand Forgeyard la
              suspend après 3 crashs en 5 minutes. L’email viendra plus tard.
            </p>
            <WebhookSteps />
            <WebhookForm
              isSet={user.notifyWebhookSet}
              onSave={async (webhook) => {
                await api.saveMyWebhook(webhook)
                onChange()
              }}
              onTest={() => api.testMyWebhook()}
              onClear={async () => {
                await api.saveMyWebhook('')
                onChange()
              }}
            />
          </section>
        )}
        {current.id === 'ssh' && <SSHKeys />}
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

/** The banner of a profile: an image, else the profile colour, else the theme's accent. */
export function Banner({ url, color, height = 140 }: { url?: string; color?: string; height?: number }) {
  return (
    <div
      className="banner-img"
      style={{ height, backgroundColor: color || undefined, backgroundImage: url ? `url("${url}")` : undefined }}
      aria-hidden="true"
    />
  )
}

/**
 * Where a picture comes from: Discord or the user's own image. Choosing Discord drops the uploaded image;
 * choosing "Mon image" asks for a file.
 */
function ImageSource({
  label,
  user,
  custom,
  fromDiscord,
  missing,
  busy,
  onUpload,
  onReset,
}: {
  label: string
  user: User
  custom: boolean
  fromDiscord: string
  missing: string
  busy: boolean
  onUpload: (f: File) => void
  onReset: () => void
}) {
  const file = useRef<HTMLInputElement>(null)
  const discord = user.method === 'discord'
  return (
    <div className="image-source">
      <div className="image-source-head">
        <strong>{label}</strong>
        {discord && (
          <div className="segmented segmented-small" role="radiogroup" aria-label={`Source de la ${label.toLowerCase()}`}>
            <button type="button" role="radio" aria-checked={!custom} className={!custom ? 'on' : ''} disabled={busy} onClick={() => custom && onReset()}>
              Discord
            </button>
            <button type="button" role="radio" aria-checked={custom} className={custom ? 'on' : ''} disabled={busy} onClick={() => file.current?.click()}>
              Mon image
            </button>
          </div>
        )}
      </div>
      <p className="muted">
        {custom ? (
          <>
            Votre image.{' '}
            <button type="button" className="link-btn" disabled={busy} onClick={() => file.current?.click()}>
              En choisir une autre
            </button>
            {!discord && (
              <>
                {' · '}
                <button type="button" className="link-btn" disabled={busy} onClick={onReset}>
                  Retirer
                </button>
              </>
            )}
          </>
        ) : discord ? (
          missing ? (
            <>
              {missing}{' '}
              <a className="link-btn" href="/api/auth/discord?return=profile">
                Récupérer maintenant
              </a>
            </>
          ) : (
            fromDiscord
          )
        ) : (
          <button type="button" className="btn" disabled={busy} onClick={() => file.current?.click()}>
            Choisir une image
          </button>
        )}
      </p>
      <input
        ref={file}
        type="file"
        accept="image/png,image/jpeg,image/webp,image/gif"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0]
          e.target.value = ''
          if (f) onUpload(f)
        }}
      />
    </div>
  )
}

function ProfilePanel({ user, onChange }: { user: User; onChange: () => void }) {
  const discord = user.method === 'discord'
  const [followDiscord, setFollowDiscord] = useState(user.nameFromDiscord)
  const [name, setName] = useState(user.displayName)
  const [bio, setBio] = useState(user.bio)
  const [email, setEmail] = useState(user.email)
  const [showApps, setShowApps] = useState(user.showApps)
  const [showEmail, setShowEmail] = useState(user.showEmail)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  // The image being framed before upload, and what it is for.
  const [cropping, setCropping] = useState<{ file: File; kind: 'avatar' | 'banner' } | null>(null)

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
    run(() => api.saveProfile({ displayName: name, nameFromDiscord: followDiscord, bio, email, showApps, showEmail }), 'Profil enregistré.')
  }

  const shownName = followDiscord && user.discordName ? user.discordName : name

  return (
    <>
      <section className="panel profile-preview">
        <Banner url={user.bannerUrl} color={user.accentColor} height={120} />
        <div className="profile-preview-body">
          <Avatar url={user.avatarUrl} name={shownName} size={76} />
          <div>
            <strong>{shownName}</strong>
            <span className="muted">{bio || 'Pas encore de description.'}</span>
          </div>
          <a className="btn btn-small" href={`#/u/${user.id}`}>
            Voir mon profil public
          </a>
        </div>
      </section>

      {error && <p className="error">{error}</p>}
      {notice && (
        <div className="banner banner-up" role="status">
          <span className="dot dot-up" />
          {notice}
        </div>
      )}

      <section className="panel">
        <h2>Apparence</h2>
        <ImageSource
          label="Photo"
          user={user}
          custom={user.customAvatar}
          fromDiscord="Votre photo Discord, mise à jour à chaque connexion."
          missing={user.avatarUrl ? '' : 'Forgeyard n’a pas encore votre photo Discord.'}
          busy={busy}
          onUpload={(f) => setCropping({ file: f, kind: 'avatar' })}
          onReset={() => run(() => api.deleteAvatar(), discord ? 'Photo Discord reprise.' : 'Photo retirée.')}
        />
        <ImageSource
          label="Bannière"
          user={user}
          custom={user.customBanner}
          fromDiscord={user.bannerUrl ? 'Votre bannière Discord, mise à jour à chaque connexion.' : 'Pas de bannière sur Discord : la couleur de votre profil Discord est utilisée.'}
          missing={user.bannerUrl || user.accentColor ? '' : 'Forgeyard n’a pas encore votre bannière Discord.'}
          busy={busy}
          onUpload={(f) => setCropping({ file: f, kind: 'banner' })}
          onReset={() => run(() => api.deleteBanner(), discord ? 'Bannière Discord reprise.' : 'Bannière retirée.')}
        />
        <small className="muted">PNG, JPEG, WebP ou GIF : vous cadrez l’image avant de l’enregistrer.</small>
      </section>

      {cropping && (
        <ImageCropper
          file={cropping.file}
          title={cropping.kind === 'avatar' ? 'Cadrer la photo' : 'Cadrer la bannière'}
          outWidth={cropping.kind === 'avatar' ? 256 : 1500}
          outHeight={cropping.kind === 'avatar' ? 256 : 500}
          round={cropping.kind === 'avatar'}
          onCancel={() => setCropping(null)}
          onSave={async (image) => {
            if (cropping.kind === 'avatar') await api.uploadAvatar(image)
            else await api.uploadBanner(image)
            setCropping(null)
            setNotice(cropping.kind === 'avatar' ? 'Photo enregistrée.' : 'Bannière enregistrée.')
            onChange()
          }}
        />
      )}

      <form className="panel" onSubmit={save}>
        <h2>Informations</h2>
        {user.username && (
          <label className="field">
            <span>Identifiant</span>
            <input value={user.username} disabled />
            <small>Il sert à vous connecter et ne change pas.</small>
          </label>
        )}
        <div className="field">
          <span>Nom affiché</span>
          {discord && (
            <div className="segmented segmented-small" role="radiogroup" aria-label="Source du nom">
              <button type="button" role="radio" aria-checked={followDiscord} className={followDiscord ? 'on' : ''} onClick={() => setFollowDiscord(true)}>
                Discord
              </button>
              <button type="button" role="radio" aria-checked={!followDiscord} className={!followDiscord ? 'on' : ''} onClick={() => setFollowDiscord(false)}>
                Mon nom
              </button>
            </div>
          )}
          <input value={shownName} onChange={(e) => setName(e.target.value)} maxLength={64} required disabled={followDiscord} aria-label="Nom affiché" />
          {followDiscord && <small>Votre nom Discord, mis à jour à chaque connexion.</small>}
        </div>
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

        <h2>Ce que voient les autres membres</h2>
        <div className="setting-row">
          <div className="header-title">
            <strong>Mes apps sur mon profil</strong>
            <p className="muted">Leur nom, leur adresse et leur état. Jamais leur configuration ni leurs logs. Chaque app peut aussi être masquée depuis sa page.</p>
          </div>
          <Switch checked={showApps} onChange={setShowApps} label="Afficher mes apps sur mon profil" />
        </div>
        <div className="setting-row">
          <div className="header-title">
            <strong>Mon email sur mon profil</strong>
            <p className="muted">Pour qu’on puisse vous écrire{discord ? ' (Discord reste proposé)' : ''}.</p>
          </div>
          <Switch checked={showEmail && email !== ''} onChange={setShowEmail} disabled={email === ''} label="Afficher mon email sur mon profil" />
        </div>

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

/** The public keys the SSH gateway lets in, for the apps one owns. */
function SSHKeys() {
  const [keys, setKeys] = useState<SSHKey[] | null>(null)
  const [publicKey, setPublicKey] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.sshKeys().then(setKeys, (err) => setError(errorMessage(err)))
  }, [])

  async function add(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const k = await api.addSSHKey(publicKey, name)
      setKeys([...(keys ?? []), k])
      setPublicKey('')
      setName('')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function remove(k: SSHKey) {
    if (!window.confirm(`Retirer la clé « ${k.name} » ? Elle ne pourra plus se connecter.`)) return
    try {
      await api.deleteSSHKey(k.id)
      setKeys((keys ?? []).filter((x) => x.id !== k.id))
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <>
      <section className="panel">
        <h2>Mes clés</h2>
        {keys && keys.length === 0 && <p className="muted">Aucune clé : ajoutez-en une ci-dessous pour vous connecter en SSH.</p>}
        {keys && keys.length > 0 && (
          <ul className="key-list">
            {keys.map((k) => (
              <li key={k.id} className="key-item">
                <span className="key-text">
                  <strong>{k.name}</strong>
                  <span className="mono muted">
                    {k.type} · {k.fingerprint}
                  </span>
                  <span className="muted key-meta">
                    Ajoutée {since(k.createdAt)} · {k.lastUsedAt ? `utilisée ${since(k.lastUsedAt)}` : 'jamais utilisée'}
                  </span>
                </span>
                <button type="button" className="btn btn-small" onClick={() => remove(k)}>
                  Retirer
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="panel">
        <h2>Ajouter une clé</h2>
        <ol className="steps-help">
          <li>
            Sur votre ordinateur, affichez votre clé publique : <CopyField value="cat ~/.ssh/id_ed25519.pub" />
          </li>
          <li>
            Pas de clé ? Créez-en une : <CopyField value="ssh-keygen -t ed25519" />
          </li>
          <li>Collez la ligne entière ci-dessous (elle commence par ssh-ed25519).</li>
        </ol>
        <form className="form" onSubmit={add}>
          <label className="field">
            <span>Clé publique</span>
            <textarea className="mono" rows={3} value={publicKey} onChange={(e) => setPublicKey(e.target.value)} placeholder="ssh-ed25519 AAAAC3Nza… moi@portable" required spellCheck={false} />
          </label>
          <label className="field">
            <span>
              Nom <span className="muted">(facultatif)</span>
            </span>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Portable perso" maxLength={60} />
          </label>
          {error && <p className="error">{error}</p>}
          <div>
            <button type="submit" className="btn btn-primary" disabled={busy || !publicKey.trim()}>
              Ajouter
            </button>
          </div>
        </form>
        <p className="muted">
          Ensuite, la page de chaque app donne sa commande : <span className="mono">ssh monapp@… -p 2222</span>. Seules vos apps acceptent vos
          clés (toutes, pour un admin).
        </p>
      </section>
    </>
  )
}

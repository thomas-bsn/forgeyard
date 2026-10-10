import { useState, type FormEvent } from 'react'
import { api, ApiError, errorMessage, type DNSProviderKind, type DomainChoice, type IngressChoice, type Instance } from './api'
import { DiscordAppSteps, DiscordIcon, Logo, ProxySnippet, ThemeToggle } from './ui'
import { IconPicker } from './Cropper'

const steps = ['Token', 'Instance', 'Domaine', 'Méthode', 'Compte admin'] as const
const DOMAIN_STEP = 2
const CALLBACK_PATH = '/api/auth/discord/callback'
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
  const [icon, setIcon] = useState('') // a framed PNG data URL, sent with the setup
  const [publicUrl, setPublicUrl] = useState(window.location.origin)
  const [localNode, setLocalNode] = useState(instance.localNodeSupported)
  const [domain, setDomain] = useState<DomainChoice>({
    mode: 'provider',
    domain: '',
    publicIp: '',
    provider: instance.dnsProviders?.[0]?.name ?? 'cloudflare',
    credentials: {},
  })
  // When the local agent found ports 80/443 taken, another proxy already runs here: suggest putting apps behind it.
  const [webPorts, setWebPorts] = useState(instance.localWebPorts)
  const [localIp, setLocalIp] = useState(instance.localIp)
  const [ingress, setIngress] = useState<IngressChoice>({ mode: instance.localWebPorts === 'busy' ? 'proxy' : 'traefik', httpPort: 8090 })
  const cleanUrl = publicUrl.trim().replace(/\/+$/, '')
  const otherOrigin = cleanUrl !== window.location.origin
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const last = step === steps.length - 1

  function next(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (step === 4 && method === 'password') {
      if (password.length < 12) return setError('Le mot de passe doit faire au moins 12 caractères.')
      if (password !== confirm) return setError('Les mots de passe ne correspondent pas.')
    }
    setStep(step + 1)
    // The local agent checks the ports while the wizard is open: ask again if it had not answered yet.
    if (step + 1 === DOMAIN_STEP && localNode && !webPorts) {
      api.instance().then((i) => {
        setWebPorts(i.localWebPorts)
        setLocalIp(i.localIp)
        if (i.localWebPorts === 'busy') setIngress((cur) => ({ ...cur, mode: 'proxy' }))
      }, () => {})
    }
  }

  async function finish(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      if (method === 'discord') {
        const { authorizeUrl } = await api.setupDiscord({ token, instanceName, publicUrl: cleanUrl, clientId, clientSecret, localNode, domain, ingress, icon: icon || undefined })
        // Discord sends the browser back to the server, which makes this account the superadmin.
        window.location.href = authorizeUrl
        return
      }
      await api.setup({ token, instanceName, publicUrl: cleanUrl, username, password, localNode, domain, ingress, icon: icon || undefined })
      onDone()
    } catch (err) {
      setError(errorMessage(err))
      // A wrong token is only detected at the end: go back to the step that needs fixing.
      if (err instanceof ApiError && err.status === 403) setStep(0)
      // Domain problems (wrong token, zone not found…) are fixed on the domain step.
      else if (err instanceof ApiError && err.status === 400 && /domaine|IP|port|fournisseur|zone|token|Cloudflare|OVH|Gandi|Porkbun/i.test(err.message)) setStep(DOMAIN_STEP)
      setBusy(false)
    }
  }

  return (
    <div className="auth-screen">
      <div className="auth-column wide">
        <div className="brand">
          <Logo size={34} src={icon || undefined} />
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
              <h2>Votre instance</h2>
              <label className="field">
                <span>Nom de votre PaaS</span>
                <input value={instanceName} onChange={(e) => setInstanceName(e.target.value)} maxLength={64} required autoFocus />
              </label>
              <div className="field">
                <span>Icône (facultatif)</span>
                <IconPicker current={icon || undefined} onPick={setIcon} />
              </div>
              <label className="field">
                <span>Adresse de Forgeyard</span>
                <input type="url" value={publicUrl} onChange={(e) => setPublicUrl(e.target.value)} placeholder="https://forgeyard.mondomaine.com" required />
                <small>L'adresse à laquelle vous et vos utilisateurs ouvrirez Forgeyard. Modifiable plus tard dans Réglages.</small>
              </label>
              {instance.localNodeSupported && (
                <label className="check">
                  <input type="checkbox" checked={localNode} onChange={(e) => setLocalNode(e.target.checked)} />
                  <span>
                    <b>Faire tourner les apps sur cette machine</b>
                    <small>Cette machine devient un node, sans rien à installer. D’autres machines pourront s’ajouter plus tard.</small>
                  </span>
                </label>
              )}
            </>
          )}

          {step === DOMAIN_STEP && (
            <DomainStep
              providers={instance.dnsProviders ?? []}
              domain={domain}
              onDomain={setDomain}
              localNode={localNode}
              webPorts={webPorts}
              localIp={localIp}
              ingress={ingress}
              onIngress={setIngress}
            />
          )}

          {step === 3 && (
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

          {step === 4 && method === 'password' && (
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

          {step === 4 && method === 'discord' && (
            <>
              <h2>Application Discord</h2>
              {otherOrigin && (
                <div className="banner banner-warn">
                  <span className="dot dot-warn" />
                  <span>
                    Vous avez choisi l'adresse <b>{cleanUrl}</b> : ouvrez ce wizard depuis cette adresse pour terminer avec Discord.
                  </span>
                </div>
              )}
              <DiscordAppSteps redirectUrl={cleanUrl + CALLBACK_PATH} />
              <label className="field">
                <span>Client ID</span>
                <input value={clientId} onChange={(e) => setClientId(e.target.value)} inputMode="numeric" placeholder="123456789012345678" required autoFocus />
              </label>
              <label className="field">
                <span>Client Secret</span>
                <input type="password" value={clientSecret} onChange={(e) => setClientSecret(e.target.value)} autoComplete="off" required />
                <small>Il est chiffré avant d'être enregistré et ne sera plus jamais affiché.</small>
              </label>
            </>
          )}

          {last && method === 'discord' && (
            <p className="muted">En terminant, vous serez envoyé sur Discord. Le compte avec lequel vous vous connectez deviendra le superadmin.</p>
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

/** The wizard's domain step: the DNS provider's API token, and which reverse proxy serves the apps. */
function DomainStep({
  providers,
  domain,
  onDomain,
  localNode,
  webPorts,
  localIp,
  ingress,
  onIngress,
}: {
  providers: DNSProviderKind[]
  domain: DomainChoice
  onDomain: (d: DomainChoice) => void
  localNode: boolean
  webPorts?: 'busy' | 'free'
  localIp?: string
  ingress: IngressChoice
  onIngress: (i: IngressChoice) => void
}) {
  const kind = providers.find((p) => p.name === domain.provider)
  const set = (patch: Partial<DomainChoice>) => onDomain({ ...domain, ...patch })
  const host = domain.domain.trim().toLowerCase() || 'mondomaine.com'
  const typed = domain.mode === 'provider' ? domain.domain.trim().toLowerCase() : ''

  return (
    <>
      <h2>Domaine de vos apps</h2>
      <p className="muted">Chaque app aura son adresse, comme monapp.{host}. Forgeyard crée lui-même son DNS.</p>
      <div className="auth-options">
        <button type="button" className={`auth-option ${domain.mode === 'provider' ? 'selected' : ''}`} onClick={() => set({ mode: 'provider' })} aria-pressed={domain.mode === 'provider'}>
          <strong>Mon domaine</strong>
          <small>Avec un token API de mon fournisseur DNS (recommandé).</small>
        </button>
        <button type="button" className={`auth-option ${domain.mode === 'none' ? 'selected' : ''}`} onClick={() => set({ mode: 'none' })} aria-pressed={domain.mode === 'none'}>
          <strong>Plus tard</strong>
          <small>Les apps seront joignables par IP et port.</small>
        </button>
      </div>

      {domain.mode === 'provider' && (
        <>
          <label className="field">
            <span>Domaine</span>
            <input value={domain.domain} onChange={(e) => set({ domain: e.target.value })} placeholder="mondomaine.com" required />
          </label>
          <label className="field">
            <span>IP publique de ce serveur</span>
            <input value={domain.publicIp} onChange={(e) => set({ publicIp: e.target.value })} placeholder="203.0.113.10" required />
            <small>Celle vers laquelle pointeront les domaines des apps.</small>
          </label>
          <label className="field">
            <span>Fournisseur DNS</span>
            <select value={domain.provider} onChange={(e) => set({ provider: e.target.value, credentials: {} })}>
              {providers.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.label}
                </option>
              ))}
            </select>
            <small>Là où sont gérés les DNS du domaine, en général là où il a été acheté.</small>
          </label>
          {kind && (
            <>
              <p className="muted">
                {kind.help}{' '}
                <a href={kind.docsUrl} target="_blank" rel="noopener noreferrer">
                  Ouvrir {kind.label}
                </a>
              </p>
              {kind.fields.map((f) => (
                <label className="field" key={f.key}>
                  <span>{f.label}</span>
                  <input
                    type={f.secret ? 'password' : 'text'}
                    value={domain.credentials[f.key] ?? ''}
                    onChange={(e) => set({ credentials: { ...domain.credentials, [f.key]: e.target.value } })}
                    placeholder={f.placeholder}
                    autoComplete="off"
                    required
                  />
                </label>
              ))}
              <small className="muted">Vérifié auprès de {kind.label} à la fin du wizard, puis chiffré.</small>
            </>
          )}
        </>
      )}

      {localNode && (
        <>
          <h2>Qui gère les ports 80 et 443 de cette machine ?</h2>
          {webPorts === 'busy' && (
            <p className="muted">Un programme répond déjà sur ces ports (sûrement votre reverse proxy) : les apps passeront derrière lui.</p>
          )}
          {webPorts === 'free' && <p className="muted">Ces ports sont libres : Forgeyard peut les prendre.</p>}
          <div className="auth-options">
            <button type="button" className={`auth-option ${ingress.mode === 'traefik' ? 'selected' : ''}`} onClick={() => onIngress({ ...ingress, mode: 'traefik' })} aria-pressed={ingress.mode === 'traefik'}>
              <strong>Forgeyard</strong>
              <small>Il prend les ports 80 et 443 et gère le HTTPS. Rien à configurer.</small>
            </button>
            <button type="button" className={`auth-option ${ingress.mode === 'proxy' ? 'selected' : ''}`} onClick={() => onIngress({ ...ingress, mode: 'proxy' })} aria-pressed={ingress.mode === 'proxy'}>
              <strong>Mon reverse proxy</strong>
              <small>Caddy, Nginx… les garde, et envoie les apps à Forgeyard.</small>
            </button>
          </div>
          {ingress.mode === 'proxy' && (
            <>
              <label className="field">
                <span>Port où Forgeyard reçoit les apps</span>
                <input type="number" min={1} max={65535} value={ingress.httpPort} onChange={(e) => onIngress({ ...ingress, httpPort: Number(e.target.value) })} required />
                <small>Laissez 8090 sauf s’il est déjà pris.</small>
              </label>
              <ProxySnippet domain={typed} port={ingress.httpPort} detectedIp={localIp} />
            </>
          )}
        </>
      )}
    </>
  )
}

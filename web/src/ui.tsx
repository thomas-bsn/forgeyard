import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'

export function Logo({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="#4f46e5" />
      <g fill="#fff">
        <rect x="7" y="7" width="7" height="7" rx="1.5" />
        <rect x="18" y="7" width="7" height="7" rx="1.5" />
        <rect x="7" y="18" width="7" height="7" rx="1.5" />
        <rect x="18" y="18" width="7" height="7" rx="1.5" />
      </g>
    </svg>
  )
}

export function DiscordIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z" />
    </svg>
  )
}

/** A value the user has to paste somewhere else, copied on click. */
export function CopyField({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      className="copy"
      onClick={() => {
        navigator.clipboard?.writeText(value)
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      }}
    >
      <code>{value}</code>
      <span className="muted">{copied ? 'Copié' : 'Copier'}</span>
    </button>
  )
}

type Theme = 'light' | 'dark' | null

function readTheme(): Theme {
  try {
    const t = localStorage.getItem('theme')
    return t === 'light' || t === 'dark' ? t : null
  } catch {
    return null
  }
}

export function applyStoredTheme() {
  const t = readTheme()
  if (t) document.documentElement.dataset.theme = t
}

/** Toggles between light and dark, starting from the system preference. */
export function ThemeToggle() {
  const [, force] = useState(0)
  const current =
    readTheme() ?? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
  const next = current === 'dark' ? 'light' : 'dark'
  return (
    <button
      type="button"
      className="icon-btn"
      aria-label={next === 'dark' ? 'Passer en thème sombre' : 'Passer en thème clair'}
      onClick={() => {
        document.documentElement.dataset.theme = next
        try {
          localStorage.setItem('theme', next)
        } catch {
          // Private browsing: the choice just isn't remembered.
        }
        force((n) => n + 1)
      }}
    >
      {next === 'dark' ? (
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
        </svg>
      ) : (
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
        </svg>
      )}
    </button>
  )
}

/** How to register Forgeyard in a Discord application, shared by the setup wizard and the settings page. */
export function DiscordAppSteps({ redirectUrl }: { redirectUrl: string }) {
  return (
    <ol className="steps-help">
      <li>
        Créez une application Discord sur{' '}
        <a href="https://discord.com/developers/applications" target="_blank" rel="noopener noreferrer">
          discord.com/developers/applications
        </a>
        .
      </li>
      <li>
        Dans l'onglet <b>OAuth2</b>, section <b>Redirects</b>, ajoutez exactement cette adresse :
        <CopyField value={redirectUrl} />
        Ouvrez toujours Forgeyard avec cette même adresse : Discord refuse les autres.
      </li>
      <li>
        Toujours dans <b>OAuth2</b>, copiez le <b>Client ID</b> et le <b>Client Secret</b> ici.
      </li>
    </ol>
  )
}

/** The local IP Forgeyard was opened with, when it was opened by IP. */
function guessLocalIp() {
  const host = window.location.hostname
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(host) && host !== '127.0.0.1' ? host : ''
}

type ProxyKind = 'caddy' | 'nginx' | 'traefik' | 'npm'

function proxyExample(kind: ProxyKind, host: string, ip: string, port: number): string {
  const target = `${ip}:${port}`
  switch (kind) {
    case 'caddy':
      return `*.${host} {\n    reverse_proxy ${target}\n}`
    case 'nginx':
      return `server {\n    listen 443 ssl;\n    server_name *.${host};\n    # ssl_certificate et ssl_certificate_key : votre certificat wildcard *.${host}\n\n    location / {\n        proxy_pass http://${target};\n        proxy_set_header Host $host;\n        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n        proxy_set_header X-Forwarded-Proto $scheme;\n    }\n}`
    case 'traefik':
      return `# Configuration dynamique (file provider)\nhttp:\n  routers:\n    forgeyard-apps:\n      rule: "HostRegexp(\`^.+\\\\.${host.replace(/\./g, '\\\\.')}$\`)"\n      service: forgeyard-apps\n      tls:\n        certResolver: votre-resolver\n        domains:\n          - main: "${host}"\n            sans: ["*.${host}"]\n  services:\n    forgeyard-apps:\n      loadBalancer:\n        servers:\n          - url: "http://${target}"`
    case 'npm':
      return `Proxy Hosts › Add Proxy Host\n  Domain Names     *.${host}\n  Scheme           http\n  Forward Hostname ${ip}\n  Forward Port     ${port}\n  Websockets       activé\nSSL › certificat wildcard *.${host} (DNS Challenge)`
  }
}

/**
 * What to add once to the user's own reverse proxy: every subdomain goes to Forgeyard's Traefik on this
 * machine, which then routes each app. The same rule works whether the proxy runs in Docker or not.
 */
export function ProxySnippet({ domain, port }: { domain: string; port: number }) {
  const [ip, setIp] = useState(guessLocalIp)
  const [kind, setKind] = useState<ProxyKind>('caddy')
  const shownIp = ip.trim() || 'IP_LOCALE'
  const host = domain || 'mondomaine.com'
  const kinds: [ProxyKind, string][] = [
    ['caddy', 'Caddy'],
    ['nginx', 'Nginx'],
    ['traefik', 'Traefik'],
    ['npm', 'Nginx Proxy Manager'],
  ]
  return (
    <>
      <label className="field">
        <span>IP locale de cette machine</span>
        <input value={ip} onChange={(e) => setIp(e.target.value)} placeholder="192.168.1.10" />
        <small>
          Celle sur votre réseau (<code>hostname -I</code>), pas 127.0.0.1 : elle marche que votre proxy tourne dans Docker ou non.
        </small>
      </label>
      <div className="proxy-rule">
        <strong>Une seule règle à ajouter à votre reverse proxy</strong>
        <div className="proxy-rule-line">
          <code>*.{host}</code>
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M5 12h14M13 6l6 6-6 6" />
          </svg>
          <code>
            http://{shownIp}:{port}
          </code>
        </div>
        <small className="muted">
          Tous les sous-domaines vers Forgeyard, avec un certificat wildcard. Ensuite chaque nouvelle app marche sans retoucher votre proxy.
        </small>
        <div className="segmented segmented-small" role="tablist" aria-label="Exemple pour">
          {kinds.map(([k, label]) => (
            <button key={k} type="button" role="tab" aria-selected={kind === k} className={kind === k ? 'on' : ''} onClick={() => setKind(k)}>
              {label}
            </button>
          ))}
        </div>
        <CopyField value={proxyExample(kind, host, shownIp, port)} />
        {kind === 'caddy' && (
          <details>
            <summary>Caddy sans module DNS pour le wildcard ?</summary>
            <CopyField
              value={`{\n    on_demand_tls {\n        ask http://${shownIp}:8080/api/caddy/ask\n    }\n}\n\nhttps:// {\n    tls {\n        on_demand\n    }\n    reverse_proxy ${shownIp}:${port}\n}`}
            />
            <small className="muted">
              Un certificat par app, créé à sa première visite, seulement si c’est une vraie app. Si votre Caddyfile a déjà un bloc{' '}
              <code>{'{ … }'}</code> tout en haut, mettez <code>on_demand_tls</code> dedans.
            </small>
          </details>
        )}
      </div>
    </>
  )
}

/**
 * A dialog over the page, closed with Escape, a click outside or the close button. With onSubmit, its
 * body and footer form one form, so Enter submits it.
 */
export function Modal({
  title,
  subtitle,
  onClose,
  onSubmit,
  footer,
  children,
  wide,
}: {
  title: ReactNode
  subtitle?: ReactNode
  onClose: () => void
  onSubmit?: (e: FormEvent) => void
  footer?: ReactNode
  children: ReactNode
  wide?: boolean
}) {
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const d = ref.current
    if (d && !d.open) d.showModal()
  }, [])
  const content = (
    <>
      <div className="modal-body">{children}</div>
      {footer && <div className="modal-foot">{footer}</div>}
    </>
  )
  return (
    <dialog
      ref={ref}
      className={`modal ${wide ? 'modal-wide' : ''}`}
      onClose={onClose}
      onCancel={(e) => {
        e.preventDefault()
        onClose()
      }}
      onMouseDown={(e) => {
        if (e.target === ref.current) onClose()
      }}
    >
      <div className="modal-head">
        <div className="header-title">
          <h2>{title}</h2>
          {subtitle && <p className="muted">{subtitle}</p>}
        </div>
        <button type="button" className="icon-btn" aria-label="Fermer" onClick={onClose}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <path d="M6 6l12 12M18 6 6 18" />
          </svg>
        </button>
      </div>
      {onSubmit ? <form onSubmit={onSubmit}>{content}</form> : content}
    </dialog>
  )
}

/** A small area chart of values over time; max sets the top of the scale (else the highest value). */
export function AreaChart({ values, max, color = 'var(--accent)', label }: { values: number[]; max?: number; color?: string; label: string }) {
  const W = 600
  const H = 120
  if (values.length < 2) {
    return <div className="chart-empty muted">Pas encore assez de mesures : elles arrivent toutes les 15 s.</div>
  }
  const top = Math.max(max ?? 0, ...values, 1e-9)
  const pts = values.map((v, i) => `${((i / (values.length - 1)) * W).toFixed(1)},${(H - (v / top) * (H - 4)).toFixed(1)}`)
  return (
    <svg className="chart" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={label}>
      <line x1="0" y1={H / 2} x2={W} y2={H / 2} className="chart-grid" />
      <polygon points={`0,${H} ${pts.join(' ')} ${W},${H}`} fill={color} opacity="0.12" />
      <polyline points={pts.join(' ')} fill="none" stroke={color} strokeWidth="2" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

/** The page shown, from the address bar's hash (#/apps/3), so links and the back button work. */
export function useRoute(): [string[], (path: string) => void] {
  const read = () =>
    window.location.hash
      .replace(/^#\/?/, '')
      .split('/')
      .filter(Boolean)
  const [route, setRoute] = useState(read)
  useEffect(() => {
    const on = () => setRoute(read())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return [route, (path: string) => (window.location.hash = '/' + path)]
}

/** Sizes in powers of 1024, as Docker counts memory limits (512 Mo = 512 × 1024²). */
export function formatBytes(n: number): string {
  const k = 1024
  if (n >= k ** 4) return `${(n / k ** 4).toFixed(1)} To`
  if (n >= k ** 3) return `${(n / k ** 3).toFixed(1)} Go`
  if (n >= k ** 2) return `${Math.round(n / k ** 2)} Mo`
  return `${Math.round(n / k)} Ko`
}

export function since(unix?: number): string {
  if (!unix) return 'jamais'
  const min = Math.round((Date.now() / 1000 - unix) / 60)
  if (min < 1) return "à l'instant"
  if (min < 60) return `il y a ${min} min`
  if (min < 48 * 60) return `il y a ${Math.round(min / 60)} h`
  return new Date(unix * 1000).toLocaleDateString('fr-FR')
}

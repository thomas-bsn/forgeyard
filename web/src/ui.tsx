import { useState } from 'react'

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

/**
 * What to add once to the user's own reverse proxy: every subdomain goes to Forgeyard's Traefik on this
 * machine, which then routes each app. The same block works whether the proxy runs in Docker or not.
 */
export function ProxySnippet({ domain, port }: { domain: string; port: number }) {
  const [ip, setIp] = useState(guessLocalIp)
  const target = `${ip.trim() || 'IP_LOCALE'}:${port}`
  const host = domain || 'mondomaine.com'
  return (
    <>
      <label className="field">
        <span>IP locale de cette machine</span>
        <input value={ip} onChange={(e) => setIp(e.target.value)} placeholder="192.168.1.10" />
        <small>
          Celle sur votre réseau (<code>hostname -I</code>), pas 127.0.0.1 : elle marche que votre proxy tourne dans Docker ou
          non.
        </small>
      </label>
      <div className="field">
        <span>À ajouter une seule fois dans votre Caddyfile, puis rechargez Caddy</span>
        <CopyField value={`*.${host} {\n    reverse_proxy ${target}\n}`} />
        <small>
          Ensuite chaque nouvelle app marche toute seule. Il faut un Caddy avec le module DNS de votre fournisseur (ligne{' '}
          <code>acme_dns</code>) pour le certificat wildcard. Avec Nginx ou un autre proxy : envoyez <code>*.{host}</code> vers{' '}
          <code>{target}</code>.
        </small>
      </div>
      <details className="field">
        <summary>Caddy sans module DNS ?</summary>
        <CopyField
          value={`{\n    on_demand_tls {\n        ask http://${ip.trim() || 'IP_LOCALE'}:8080/api/caddy/ask\n    }\n}\n\nhttps:// {\n    tls {\n        on_demand\n    }\n    reverse_proxy ${target}\n}`}
        />
        <small>
          Un certificat par app, créé à sa première visite, seulement si c’est une vraie app. Si votre Caddyfile a déjà un bloc{' '}
          <code>{'{ … }'}</code> tout en haut, mettez <code>on_demand_tls</code> dedans.
        </small>
      </details>
    </>
  )
}

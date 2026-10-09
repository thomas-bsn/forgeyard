import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type DomainCheck, type DomainMode, type DomainSettings } from './api'
import { CopyField } from './ui'

const modes: { value: DomainMode; title: string; text: string }[] = [
  { value: 'none', title: 'Aucun', text: 'Les apps sont accessibles par IP et port.' },
  { value: 'wildcard', title: 'Wildcard manuel', text: 'Un seul enregistrement DNS, chez n’importe quel fournisseur.' },
  { value: 'cloudflare', title: 'Cloudflare', text: 'Forgeyard crée lui-même les enregistrements via l’API.' },
]

/** Superadmin only: Forgeyard's address and how app subdomains reach the servers. */
export default function DomainPanel() {
  const [saved, setSaved] = useState<DomainSettings | null>(null)
  const [publicUrl, setPublicUrl] = useState('')
  const [mode, setMode] = useState<DomainMode>('none')
  const [domain, setDomain] = useState('')
  const [publicIp, setPublicIp] = useState('')
  const [token, setToken] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [check, setCheck] = useState<DomainCheck | null>(null)
  const [busy, setBusy] = useState(false)

  function apply(s: DomainSettings) {
    setSaved(s)
    setPublicUrl(s.publicUrl)
    setMode(s.mode)
    setDomain(s.domain)
    setPublicIp(s.publicIp)
    setToken('')
  }

  useEffect(() => {
    api.domainSettings().then(apply).catch((err) => setError(errorMessage(err)))
  }, [])

  async function save(e: FormEvent) {
    e.preventDefault()
    if (!saved) return
    setBusy(true)
    setError('')
    setNotice('')
    setCheck(null)
    try {
      const s = await api.saveDomainSettings({ publicUrl, mode, domain, publicIp, cloudflareToken: token })
      const addressChanged = s.publicUrl !== saved.publicUrl
      apply(s)
      setNotice(
        addressChanged
          ? 'Enregistré. L’adresse a changé : mettez à jour la redirection de votre application Discord (ci-dessous) et ouvrez désormais Forgeyard depuis la nouvelle adresse.'
          : 'Enregistré.',
      )
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function runCheck() {
    setBusy(true)
    setCheck(null)
    try {
      setCheck(await api.checkDomain())
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!saved) return error ? <p className="error">{error}</p> : null
  const dirty =
    publicUrl !== saved.publicUrl || mode !== saved.mode || domain !== saved.domain || publicIp !== saved.publicIp || token !== ''
  const host = domain.trim().toLowerCase() || 'mondomaine.com'

  return (
    <form className="panel" onSubmit={save}>
      <h2>Adresse et domaine</h2>

      <label className="field">
        <span>Adresse de Forgeyard</span>
        <input type="url" value={publicUrl} onChange={(e) => setPublicUrl(e.target.value)} placeholder="https://forgeyard.mondomaine.com" required />
        <small>Utilisée pour la connexion Discord et les commandes des nodes.</small>
      </label>

      <div className="field">
        <span>Domaine des apps</span>
        <div className="auth-options">
          {modes.map((m) => (
            <button
              key={m.value}
              type="button"
              className={`auth-option ${mode === m.value ? 'selected' : ''}`}
              onClick={() => setMode(m.value)}
              aria-pressed={mode === m.value}
            >
              <strong>{m.title}</strong>
              <small>{m.text}</small>
            </button>
          ))}
        </div>
      </div>

      {mode !== 'none' && (
        <>
          <label className="field">
            <span>Domaine</span>
            <input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="mondomaine.com" required />
            <small>Les apps auront une adresse comme monapp.{host}.</small>
          </label>
          <label className="field">
            <span>IP publique du serveur</span>
            <input value={publicIp} onChange={(e) => setPublicIp(e.target.value)} placeholder="203.0.113.10" required />
          </label>
        </>
      )}

      {mode === 'wildcard' && (
        <div className="field">
          <span>Enregistrement à créer chez votre fournisseur DNS</span>
          <CopyField value={`*.${host}  A  ${publicIp.trim() || '<IP publique>'}`} />
          <small>Une seule fois. Il peut mettre quelques minutes à se propager.</small>
        </div>
      )}

      {mode === 'cloudflare' && (
        <>
          <ol className="steps-help">
            <li>
              Les DNS du domaine doivent être gérés par Cloudflare (le domaine peut être acheté ailleurs).
            </li>
            <li>
              Créez un token sur{' '}
              <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noopener noreferrer">
                dash.cloudflare.com/profile/api-tokens
              </a>{' '}
              avec le modèle <b>Edit zone DNS</b>, limité à votre domaine.
            </li>
          </ol>
          <label className="field">
            <span>Token API Cloudflare</span>
            <input
              type="password"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder={saved.cloudflareHasToken ? '•••••••• (inchangé)' : ''}
              autoComplete="off"
              required={!saved.cloudflareHasToken}
            />
            <small>
              Chiffré avant d’être enregistré, jamais réaffiché.
              {saved.cloudflareZone ? ` Zone actuelle : ${saved.cloudflareZone}.` : ''}
            </small>
          </label>
        </>
      )}

      <div className="field">
        <span>Redirection Discord</span>
        <CopyField value={saved.discordRedirectUrl} />
      </div>

      {error && <p className="error">{error}</p>}
      {notice && <p className="muted">{notice}</p>}
      {check && (
        <div className={`banner ${check.ok ? 'banner-up' : 'banner-warn'}`} role="status">
          <span className={`dot ${check.ok ? 'dot-up' : 'dot-warn'}`} />
          <span>
            {check.message}
            {check.resolved && check.resolved.length > 0 ? ` (${check.name} → ${check.resolved.join(', ')})` : ''}
          </span>
        </div>
      )}

      <div className="panel-footer">
        {saved.mode !== 'none' && (
          <button type="button" className="btn" onClick={runCheck} disabled={busy || dirty}>
            Vérifier
          </button>
        )}
        <span className="spacer" />
        <button type="submit" className="btn btn-primary" disabled={busy || !dirty}>
          {busy ? 'Vérification…' : 'Enregistrer'}
        </button>
      </div>
    </form>
  )
}

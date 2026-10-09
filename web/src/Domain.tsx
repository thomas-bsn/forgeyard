import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type DomainCheck, type DomainMode, type DomainSettings } from './api'
import { CopyField } from './ui'

const modes: { value: DomainMode; title: string; text: string }[] = [
  { value: 'none', title: 'Aucun', text: 'Les apps sont accessibles par IP et port.' },
  { value: 'wildcard', title: 'Wildcard manuel', text: 'Un seul enregistrement DNS, chez n’importe quel fournisseur.' },
  { value: 'provider', title: 'API du fournisseur', text: 'Forgeyard crée lui-même un enregistrement par app.' },
]

/** Superadmin only: Forgeyard's address and how app subdomains reach the servers. */
export default function DomainPanel() {
  const [saved, setSaved] = useState<DomainSettings | null>(null)
  const [publicUrl, setPublicUrl] = useState('')
  const [mode, setMode] = useState<DomainMode>('none')
  const [domain, setDomain] = useState('')
  const [publicIp, setPublicIp] = useState('')
  const [provider, setProvider] = useState('cloudflare')
  const [creds, setCreds] = useState<Record<string, string>>({})
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
    setProvider(s.provider || 'cloudflare')
    setCreds(s.credentials)
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
      const s = await api.saveDomainSettings({ publicUrl, mode, domain, publicIp, provider, credentials: creds })
      const addressChanged = s.publicUrl !== saved.publicUrl
      apply(s)
      setNotice(
        (addressChanged
          ? 'Enregistré. L’adresse a changé : mettez à jour la redirection de votre application Discord (ci-dessous) et ouvrez désormais Forgeyard depuis la nouvelle adresse.'
          : 'Enregistré. Les apps existantes ont été mises à jour.') + (s.warning ? ` Attention : ${s.warning}` : ''),
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
  const kind = saved.providers.find((p) => p.name === provider)
  const sameProvider = provider === saved.provider
  const credsChanged = JSON.stringify(creds) !== JSON.stringify(sameProvider ? saved.credentials : {})
  const dirty =
    publicUrl !== saved.publicUrl ||
    mode !== saved.mode ||
    domain !== saved.domain ||
    publicIp !== saved.publicIp ||
    (mode === 'provider' && (!sameProvider || credsChanged))
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

      {mode === 'provider' && (
        <>
          <label className="field">
            <span>Fournisseur DNS</span>
            <select
              value={provider}
              onChange={(e) => {
                setProvider(e.target.value)
                setCreds(e.target.value === saved.provider ? saved.credentials : {})
              }}
            >
              {saved.providers.map((p) => (
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
              {kind.fields.map((f) => {
                const stored = sameProvider && f.secret && saved.secretsSet.includes(f.key)
                return (
                  <label className="field" key={f.key}>
                    <span>{f.label}</span>
                    <input
                      type={f.secret ? 'password' : 'text'}
                      value={creds[f.key] ?? ''}
                      onChange={(e) => setCreds({ ...creds, [f.key]: e.target.value })}
                      placeholder={stored ? '•••••••• (inchangé)' : f.placeholder}
                      autoComplete="off"
                      required={!stored}
                    />
                  </label>
                )
              })}
              <small className="muted">
                Les identifiants sont chiffrés avant d’être enregistrés et jamais réaffichés.
                {saved.zone && sameProvider ? ` Zone actuelle : ${saved.zone}.` : ''}
              </small>
            </>
          )}
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

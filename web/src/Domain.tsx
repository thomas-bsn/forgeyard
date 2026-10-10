import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type DomainCheck, type DomainMode, type DomainSettings } from './api'
import { CopyField } from './ui'

const modes: { value: DomainMode; title: string; text: string }[] = [
  { value: 'none', title: 'Aucun', text: 'Les apps sont accessibles par IP et port.' },
  { value: 'wildcard', title: 'Wildcard manuel', text: 'Un seul enregistrement DNS, chez n’importe quel fournisseur.' },
  { value: 'provider', title: 'API du fournisseur', text: 'Forgeyard crée lui-même un enregistrement par app.' },
]

/** Superadmin only: how app subdomains reach the servers. Forgeyard's own address is in General settings. */
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
          ? 'Enregistré.'
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
    mode !== saved.mode ||
    domain !== saved.domain ||
    publicIp !== saved.publicIp ||
    (mode === 'provider' && (!sameProvider || credsChanged))
  const host = domain.trim().toLowerCase() || 'mondomaine.com'

  const savedKind = saved.providers.find((p) => p.name === saved.provider)

  return (
    <>
      <section className="panel summary-card">
        <div className="summary-head">
          <span className={`dot ${saved.mode === 'none' ? '' : check ? (check.ok ? 'dot-up' : 'dot-warn') : 'dot-up'}`} />
          <div className="header-title">
            <strong>
              {saved.mode === 'none'
                ? 'Pas de domaine : les apps sont joignables par IP et port'
                : saved.mode === 'provider'
                  ? `${saved.domain} · ${savedKind?.label ?? saved.provider}`
                  : `${saved.domain} · wildcard manuel`}
            </strong>
            {check && <p className="muted">{check.message}</p>}
          </div>
          {saved.mode !== 'none' && (
            <button type="button" className="btn" onClick={runCheck} disabled={busy}>
              Vérifier
            </button>
          )}
        </div>
        {saved.mode !== 'none' && (
          <div className="tiles">
            <div className="tile">
              <div className="k">Adresse d’une app</div>
              <div className="v v-small">monapp.{saved.domain}</div>
            </div>
            <div className="tile">
              <div className="k">IP publique</div>
              <div className="v v-small">{saved.publicIp || '–'}</div>
            </div>
            <div className="tile">
              <div className="k">DNS</div>
              <div className="v v-small">{saved.mode === 'provider' ? 'créés par Forgeyard' : 'gérés par vous'}</div>
            </div>
          </div>
        )}
      </section>

    <form className="panel" onSubmit={save}>
      <h2>Modifier</h2>

      <div className="field">
        <span>Mode</span>
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

      {error && <p className="error">{error}</p>}
      {notice && <p className="muted">{notice}</p>}
      <div className="panel-footer">
        <span className="spacer" />
        <button type="submit" className="btn btn-primary" disabled={busy || !dirty}>
          {busy ? 'Vérification…' : 'Enregistrer'}
        </button>
      </div>
    </form>
    </>
  )
}

import { useEffect, useState } from 'react'
import { api, errorMessage, type BindRequest, type BindMount } from './api'
import { since } from './ui'

const statusLabels: Record<string, string> = { pending: 'En attente du superadmin', cancelled: 'Annulée', refused: 'Refusée', approving: 'Approbation et création en cours', failed: 'Création impossible', created: 'App créée' }

export default function BindRequests({ superadmin, route, go }: { superadmin: boolean; route: string[]; go: (path: string) => void }) {
  const review = superadmin && route[1] === 'review'
  const selected = Number(review ? route[2] : route[1]) || 0
  const [requests, setRequests] = useState<BindRequest[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [reason, setReason] = useState('')
  const [validated, setValidated] = useState<BindMount[] | null>(null)
  const [secrets, setSecrets] = useState<Record<string, string> | null>(null)
  const request = requests.find((r) => r.id === selected)
  const load = () => api.bindRequests(review).then((rows) => { setRequests(rows); setError('') }, (err) => setError(errorMessage(err)))
  useEffect(() => {
    let live = true
    const refresh = () => api.bindRequests(review).then((rows) => { if (live) { setRequests(rows); setError('') } }, (err) => live && setError(errorMessage(err)))
    refresh()
    const timer = setInterval(refresh, 15000)
    return () => { live = false; clearInterval(timer) }
  }, [review])
  useEffect(() => { setSecrets(null); setReason(''); setValidated(null) }, [selected, review])
  async function act(action: () => Promise<unknown>) {
    setBusy(true); setError('')
    try { await action(); await load() } catch (err) { setError(errorMessage(err)) }
    finally { setBusy(false) }
  }
  const base = review ? 'bind-requests/review' : 'bind-requests'
  return <div className="section">
    <div className="section-head"><h1>{review ? 'Demandes à examiner' : 'Mes demandes'}</h1>
      {superadmin && <a className="btn" href={`#/${review ? 'bind-requests' : 'bind-requests/review'}`}>{review ? 'Mes demandes' : 'File du superadmin'}</a>}
    </div>
    {error && <p className="error" role="alert">{error}</p>}
    {!selected ? <div className="panel">
      {requests.length === 0 && <p className="muted">Aucune demande.</p>}
      {requests.map((r) => <a className="ticket-item" key={r.id} href={`#/${base}/${r.id}`}><span className="ticket-item-text"><strong>{r.config.name}</strong><span className="muted">{statusLabels[r.status] ?? r.status} · {r.nodeName} · {review ? `${r.authorName} · ` : ''}{since(r.createdAt)}</span></span></a>)}
    </div> : request ? <article className="panel">
      <button type="button" className="link-button" onClick={() => go(base)}>← Retour aux demandes</button>
      <h2>{request.config.name}</h2>
      <p>{statusLabels[request.status] ?? request.status}</p>
      {request.appId && <p><a className="btn" href={`#/apps/${request.appId}`}>Ouvrir l’app</a> · Déploiement : {request.deploymentState ?? 'en attente'}</p>}
      {request.deploymentError && <p className="error" role="alert">{request.deploymentError}</p>}
      {review && (request.status === 'pending' || request.status === 'failed') && <div className="actions">
        <button className="btn" type="button" disabled={busy} onClick={() => act(async () => setValidated(await api.validateBindRequest(request.id)))}>Vérifier les chemins sur le node</button>
        <button className="btn btn-primary" type="button" disabled={busy} onClick={() => act(() => request.status === 'failed' ? api.retryBindRequest(request.id) : api.approveBindRequest(request.id))}>{request.status === 'failed' ? 'Relancer la création' : 'Approuver et créer'}</button>
      </div>}
      {validated && <div><h3>Chemins vérifiés par l’agent</h3><p className="muted">L’approbation vérifiera à nouveau les chemins.</p><ul>{validated.map((m,i) => <li key={i}><code>{m.source}</code> → <code>{m.target}</code> ({m.readOnly ? 'lecture seule' : 'lecture / écriture'})</li>)}</ul></div>}
      {request.reason && <p><strong>Motif :</strong> {request.reason}</p>}
      <dl className="aside-facts"><dt>Auteur</dt><dd>{request.authorName}</dd><dt>Node</dt><dd>{request.nodeName}</dd><dt>Image</dt><dd>{request.config.image}</dd><dt>Port</dt><dd>{request.config.port}</dd><dt>Mémoire</dt><dd>{request.config.memoryMb} Mo</dd></dl>
      <h3>Montages demandés</h3>
      <div className="table-scroll"><table><thead><tr><th>Source sur l’hôte</th><th>Destination</th><th>Accès</th></tr></thead><tbody>{request.config.bindMounts.map((m,i) => <tr key={i}><td><code>{m.source}</code></td><td><code>{m.target}</code></td><td>{m.readOnly ? 'Lecture seule' : 'Lecture / écriture'}</td></tr>)}</tbody></table></div>
      {!!request.config.volumes?.length && <p>Volumes Docker : {request.config.volumes.map((v) => <code key={v}>{v} </code>)}</p>}
      {request.config.gpu && <p>GPU : {request.config.gpu.all ? 'tous les GPU NVIDIA du node' : request.config.gpu.ids?.join(', ')}</p>}
      {request.config.logoImage && <img className="app-logo" width={64} height={64} src={request.config.logoImage} alt="Logo soumis" />}
      <p>Variables conservées : {request.envKeys.join(', ') || 'aucune'}. Leurs valeurs ne sont pas affichées.</p>
      {superadmin && request.envKeys.length > 0 && <>
        <button className="btn" type="button" disabled={busy} onClick={() => secrets ? setSecrets(null) : act(async () => setSecrets(await api.revealBindRequest(request.id)))}>{secrets ? 'Masquer les variables secrètes' : 'Révéler les variables secrètes'}</button>
        {secrets && <pre className="mono">{JSON.stringify(secrets, null, 2)}</pre>}
      </>}
      {request.status === 'pending' && (review ? <form onSubmit={(e) => { e.preventDefault(); act(() => api.refuseBindRequest(request.id, reason)) }}>
        <label className="field"><span>Motif du refus</span><textarea required maxLength={2000} value={reason} onChange={(e) => setReason(e.target.value)} /></label>
        <button className="btn" type="submit" disabled={busy || !reason.trim()}>Refuser la demande</button>
      </form> : <button className="btn" type="button" disabled={busy} onClick={() => act(() => api.cancelBindRequest(request.id))}>Annuler ma demande</button>)}
      <p className="muted">Cette configuration est figée. Pour la changer, annule la demande en attente et soumets une nouvelle configuration.</p>
    </article> : <p className="muted">Demande introuvable ou chargement en cours.</p>}
  </div>
}

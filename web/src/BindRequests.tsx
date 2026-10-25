import { useEffect, useState } from 'react'
import { api, errorMessage, type BindRequest, type BindMount } from './api'

const statusLabels: Record<string, string> = { pending: 'En attente du superadmin', cancelled: 'Annulée', refused: 'Refusée', approving: 'Approbation et création en cours', failed: 'Création impossible', created: 'Créée' }

export const bindStatusLabels = statusLabels
const statusTone: Record<string, string> = { pending: 'warn', approving: 'warn', created: 'up', failed: 'down', refused: 'down' }

/** A request to mount folders of a node, shown in the Support: what it asks, and for a superadmin the decision. */
export function BindRequestDetail({ id, superadmin, onChange }: { id: number; superadmin: boolean; onChange: () => void }) {
  const [request, setRequest] = useState<BindRequest | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [reason, setReason] = useState('')
  const [validated, setValidated] = useState<BindMount[] | null>(null)
  const [secrets, setSecrets] = useState<Record<string, string> | null>(null)
  const requestName = request?.config.stack?.name ?? request?.config.name ?? 'Demande'
  const load = () => api.bindRequest(id).then((r) => { setRequest(r); setError('') }, (err) => setError(errorMessage(err)))
  useEffect(() => {
    let live = true
    const refresh = () => api.bindRequest(id).then((r) => { if (live) { setRequest(r); setError('') } }, (err) => live && setError(errorMessage(err)))
    refresh()
    const timer = setInterval(refresh, 15000)
    return () => { live = false; clearInterval(timer) }
  }, [id])
  async function act(action: () => Promise<unknown>) {
    setBusy(true); setError('')
    try { await action(); await load(); onChange() } catch (err) { setError(errorMessage(err)) }
    finally { setBusy(false) }
  }
  if (!request) return error ? <p className="error" role="alert">{error}</p> : <p className="muted">Demande introuvable ou chargement en cours.</p>
  return <>
    {error && <p className="error" role="alert">{error}</p>}
    <article className="panel bind-request">
      <div className="bind-request-head"><div><span className="bind-request-kind">Accès aux dossiers</span><h2>{requestName}</h2></div><span className={`overview-status ${statusTone[request.status] ?? ''}`}>{statusLabels[request.status] ?? request.status}</span></div>
      {request.appId && <p><a className="btn" href={`#/apps/${request.appId}`}>Ouvrir l’app</a> · Déploiement : {request.deploymentState ?? 'en attente'}</p>}
      {request.stackId && <p><a className="btn" href={`#/projects/${request.stackId}`}>Ouvrir le projet</a></p>}
      {request.config.change && request.status === 'created' && <p><a className="btn" href={request.config.change.resourceType === 'stack' ? `#/projects/${request.config.change.resourceId}` : `#/apps/${request.config.change.resourceId}`}>Ouvrir la ressource modifiée</a></p>}
      {request.deploymentError && <p className="error" role="alert">{request.deploymentError}</p>}
      {superadmin && (request.status === 'pending' || request.status === 'failed') && <div className="actions">
        <button className="btn" type="button" disabled={busy} onClick={() => act(async () => setValidated(await api.validateBindRequest(request.id)))}>Vérifier les chemins sur le node</button>
        <button className="btn btn-primary" type="button" disabled={busy} onClick={() => act(() => request.status === 'failed' ? api.retryBindRequest(request.id) : api.approveBindRequest(request.id))}>{request.status === 'failed' ? 'Relancer la création' : 'Approuver et créer'}</button>
      </div>}
      {validated && <div><h3>Chemins vérifiés par l’agent</h3><p className="muted">L’approbation vérifiera à nouveau les chemins.</p><ul>{validated.map((m,i) => <li key={i}>{m.service && <strong>{m.service}: </strong>}<code>{m.source}</code> → <code>{m.target}</code> ({m.readOnly ? 'lecture seule' : 'lecture / écriture'})</li>)}</ul></div>}
      {request.reason && <p><strong>Motif :</strong> {request.reason}</p>}
      <dl className="aside-facts"><dt>Auteur</dt><dd>{request.authorName}</dd><dt>Node</dt><dd>{request.nodeName}</dd>{request.config.stack ? <><dt>Type</dt><dd>Projet Compose</dd><dt>Services</dt><dd>{request.config.stack.compose.match(/^\s{2}[a-z0-9][a-z0-9_-]*:/gm)?.length ?? '—'}</dd></> : <><dt>Source</dt><dd>{request.config.kind === 'sandbox' ? 'Sandbox · SSH' : request.config.dockerfile ? 'Dockerfile' : 'Image Docker'}</dd>{request.config.dockerfile ? <><dt>Dockerfile</dt><dd><pre className="mono">{request.config.dockerfile}</pre></dd></> : <><dt>Image</dt><dd>{request.config.image}</dd></>}<dt>Port</dt><dd>{request.config.kind === 'sandbox' ? 'Aucune adresse web' : request.config.port}</dd><dt>Mémoire</dt><dd>{request.config.memoryMb} Mo</dd></>}</dl>
      {request.config.stack && <details className="field"><summary>Compose soumis</summary><pre className="mono">{request.config.stack.compose}</pre></details>}
      {request.config.change && <p className="hint">Modification de {request.config.change.resourceType === 'stack' ? 'projet' : 'app'} #{request.config.change.resourceId} · node cible : {request.nodeName}. La configuration actuelle reste active jusqu’à l’approbation.</p>}
      <h3>Montages demandés</h3>
      <div className="table-scroll"><table><thead><tr>{request.config.stack && <th>Service</th>}<th>Source sur l’hôte</th><th>Destination</th><th>Accès</th></tr></thead><tbody>{request.config.bindMounts.map((m,i) => <tr key={i}>{request.config.stack && <td>{m.service}</td>}<td><code>{m.source}</code></td><td><code>{m.target}</code></td><td>{m.readOnly ? 'Lecture seule' : 'Lecture / écriture'}</td></tr>)}</tbody></table></div>
      {!!request.config.volumes?.length && <p>Volumes Docker : {request.config.volumes.map((v) => <code key={v}>{v} </code>)}</p>}
      {request.config.gpu && <p>GPU : {request.config.gpu.all ? 'tous les GPU NVIDIA du node' : request.config.gpu.ids?.join(', ')}</p>}
      {request.config.logoImage && <img className="app-logo" width={64} height={64} src={request.config.logoImage} alt="Logo soumis" />}
      <p>Variables conservées : {request.envKeys.join(', ') || 'aucune'}. Leurs valeurs ne sont pas affichées.</p>
      {superadmin && request.envKeys.length > 0 && <>
        <button className="btn" type="button" disabled={busy} onClick={() => secrets ? setSecrets(null) : act(async () => setSecrets(await api.revealBindRequest(request.id)))}>{secrets ? 'Masquer les variables secrètes' : 'Révéler les variables secrètes'}</button>
        {secrets && <pre className="mono">{JSON.stringify(secrets, null, 2)}</pre>}
      </>}
      {request.status === 'pending' && (superadmin ? <form onSubmit={(e) => { e.preventDefault(); act(() => api.refuseBindRequest(request.id, reason)) }}>
        <label className="field"><span>Motif du refus</span><textarea required maxLength={2000} value={reason} onChange={(e) => setReason(e.target.value)} /></label>
        <button className="btn" type="submit" disabled={busy || !reason.trim()}>Refuser la demande</button>
      </form> : <button className="btn" type="button" disabled={busy} onClick={() => act(() => api.cancelBindRequest(request.id))}>Annuler ma demande</button>)}
      {!superadmin && request.status === 'failed' && request.config.change && <button className="btn" type="button" disabled={busy} onClick={() => act(() => api.cancelBindRequest(request.id))}>Annuler cette demande en échec</button>}
      {request.status === 'pending' && <p className="muted">Cette configuration est figée. Pour la changer, annule la demande en attente et refais-en une.</p>}
    </article>
  </>
}

import { useEffect, useState } from 'react'
import { api, errorMessage, type VisibleNode } from './api'
import { formatBytes, since } from './ui'

export default function VisibleNodes() {
  const [nodes, setNodes] = useState<VisibleNode[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    const load = () => api.visibleNodes().then((value) => {
      if (live) { setNodes(value); setError('') }
    }, (err) => { if (live) setError(errorMessage(err)) })
    load()
    const timer = setInterval(load, 5000)
    return () => { live = false; clearInterval(timer) }
  }, [])

  return <section className="section">
    <div className="toolbar"><h1 className="toolbar-title">Nodes <span className="muted">Machines de l’instance</span></h1></div>
    <p className="muted">État et capacité des machines. Cette page est en lecture seule.</p>
    {error && <p className="error">{error}</p>}
    {nodes?.length === 0 && <div className="empty-state"><strong>Aucun node</strong></div>}
    {nodes && nodes.length > 0 && <div className="node-grid">{nodes.map((node) => <article className="card node-card" key={node.id}>
      <div className="node-card-head"><strong>{node.name}</strong><span className={`badge ${node.state === 'online' ? 'green' : ''}`}>{node.state === 'online' ? 'En ligne' : node.state === 'pending' ? 'En attente' : 'Hors ligne'}</span></div>
      <p>{[node.os, node.arch, node.dockerVersion && `Docker ${node.dockerVersion}`].filter(Boolean).join(' · ') || 'Informations système indisponibles'}</p>
      <dl className="node-facts"><div><dt>Processeurs</dt><dd>{node.cpus || '—'}</dd></div><div><dt>Mémoire</dt><dd>{formatBytes(node.memoryBytes)}</dd></div><div><dt>Disque</dt><dd>{formatBytes(node.diskBytes)}</dd></div>
        {node.metrics && <><div><dt>CPU utilisé</dt><dd>{node.metrics.cpuPercent.toFixed(1)}%</dd></div><div><dt>Mémoire utilisée</dt><dd>{formatBytes(node.metrics.memoryUsedBytes)}</dd></div><div><dt>Apps actives</dt><dd>{node.metrics.containersRunning}</dd></div></>}
      </dl>
      <small className="muted">{node.lastSeenAt ? `Vu ${since(node.lastSeenAt)}` : 'Aucun signal reçu'}</small>
    </article>)}</div>}
  </section>
}

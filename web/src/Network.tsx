import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { api, errorMessage, type App, type AppNetwork, type Diagnosis, type PathStep, type ProbeResult, type Topology, type TopoContainer, type TopoNode } from './api'
import { since } from './ui'

// How requests reach apps, and how the infrastructure is wired: an app's Réseau tab (its request path,
// checked step by step), and for admins the Topologie and Tableau views of the Nodes page.
// Sharing a Docker network means two containers can reach each other, not that they do: the views say so.

const POLL_MS = 10000

const icons: Record<PathStep['key'], ReactNode> = {
  visitor: <path d="M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Zm-9 9h18M12 3c2.5 2.5 3.5 5.5 3.5 9s-1 6.5-3.5 9c-2.5-2.5-3.5-5.5-3.5-9s1-6.5 3.5-9Z" />,
  dns: <path d="M4 6h16M4 12h16M4 18h10M18 16l2 2-2 2" />,
  entry: <path d="M3 11 12 4l9 7v9H3Zm6 9v-6h6v6" />,
  relay: <path d="M4 7h13l-3-3M20 17H7l3 3" />,
  traefik: <path d="M12 3v18M5 8h14l-2 3H5Zm2 6h12v3H7Z" />,
  container: <path d="M3 7 12 3l9 4v10l-9 4-9-4Zm0 0 9 4 9-4M12 11v10" />,
  gateway: <path d="M15 7a4 4 0 1 1-3.5 6L5 19.5V22H2v-3l6.5-6.5A4 4 0 0 1 15 7Zm1 2h.01" />,
  node: <path d="M3 4h18v7H3Zm0 9h18v7H3Zm4-5.5h.01M7 16.5h.01" />,
}

function StepIcon({ k }: { k: PathStep['key'] }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {icons[k]}
    </svg>
  )
}

const stateTone: Record<PathStep['state'], string> = { ok: 'up', warn: 'warn', error: 'down', info: '', unknown: '' }

/** One hop of a request: what it is, where it sends, and whether it works. */
function Step({ step, compact }: { step: PathStep; compact?: boolean }) {
  return (
    <div className={`path-step path-${step.state} ${compact ? 'path-step-compact' : ''}`}>
      <span className="path-step-head">
        <span className="path-icon">
          <StepIcon k={step.key} />
        </span>
        <strong>{step.title}</strong>
      </span>
      {step.detail && <span className="mono path-detail">{step.detail}</span>}
      <span className={`path-note ${stateTone[step.state] ? `state-${stateTone[step.state]}` : 'muted'}`}>
        {step.state !== 'info' && <span className={`dot ${stateTone[step.state] ? `dot-${stateTone[step.state]}` : ''}`} />}
        {step.note}
      </span>
    </div>
  )
}

/** The hops of a request in a row, linked; the link into a failing hop is red. */
export function RequestPath({ steps, compact }: { steps: PathStep[]; compact?: boolean }) {
  return (
    <div className={`request-path ${compact ? 'request-path-compact' : ''}`}>
      {steps.map((s, i) => (
        <div key={s.key + i} className="path-cell">
          {i > 0 && <span className={`path-link ${s.state === 'error' ? 'path-link-error' : ''}`} aria-hidden="true" />}
          <Step step={s} compact={compact} />
        </div>
      ))}
    </div>
  )
}

function Issue({ d, onFix, busy }: { d: Diagnosis; onFix?: (port: number) => void; busy?: boolean }) {
  return (
    <div className={`issue issue-${d.level}`}>
      <div className="issue-text">
        <strong>{d.message}</strong>
        {d.help && <span className="muted">{d.help}</span>}
      </div>
      {d.fixPort && onFix && (
        <button type="button" className="btn btn-primary" disabled={busy} onClick={() => onFix(d.fixPort!)}>
          Utiliser {d.fixPort}
        </button>
      )}
    </div>
  )
}

/** Sets an app's port, keeping the rest of its configuration. */
async function setAppPort(appId: number, port: number) {
  const a = await api.app(appId)
  await api.updateApp(appId, { image: a.dockerfile ? '' : a.image, dockerfile: a.dockerfile ?? '', port, memoryMb: a.memoryMb, env: a.env ?? {} })
}

/** The app page's Réseau tab: the request path, its problems, and the app's place on its node's networks. */
export function AppNetworkTab({ app, onChange }: { app: App; onChange: () => void }) {
  const [net, setNet] = useState<AppNetwork | null>(null)
  const [probe, setProbe] = useState<ProbeResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const load = () => api.appNetwork(app.id).then(setNet, (err) => setError(errorMessage(err)))
  useEffect(() => {
    load()
    const t = setInterval(load, POLL_MS)
    return () => clearInterval(t)
  }, [app.id, app.updatedAt])

  async function test() {
    setBusy(true)
    setProbe(null)
    try {
      setProbe(await api.probeApp(app.id))
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  async function fix(port: number) {
    setBusy(true)
    try {
      await setAppPort(app.id, port)
      onChange()
      await load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!net) return error ? <p className="error">{error}</p> : <div className="empty-state">Vérification du chemin…</div>
  const aliases = net.networks.flatMap((n) => n.aliases ?? [])
  const alias = aliases.includes(app.name) ? app.name : (aliases[0] ?? app.name)

  return (
    <div className="network-tab">
      <section className="panel path-panel">
        <div className="path-panel-head">
          <strong>{net.kind === 'sandbox' ? 'Chemin d’une connexion SSH' : 'Chemin d’une requête'}</strong>
          <span className="muted">
            {net.url ?? ''} · vérifié {since(net.checkedAt)}
          </span>
          {net.url && (
            <button type="button" className="btn btn-small" disabled={busy} onClick={test}>
              Tester maintenant
            </button>
          )}
        </div>
        <RequestPath steps={net.steps} />
        {probe && (
          <p className={`probe ${probe.ok ? 'state-up' : 'state-down'}`}>
            {probe.status
              ? `Réponse ${probe.status} en ${probe.ms} ms, depuis la machine de Forgeyard.`
              : `Pas de réponse depuis la machine de Forgeyard : ${probe.error}`}
          </p>
        )}
        {net.issues.map((d, i) => (
          <Issue key={i} d={d} onFix={fix} busy={busy} />
        ))}
        {net.issues.length === 0 && <p className="muted path-allgood">Toutes les étapes vérifiables sont bonnes.</p>}
        {error && <p className="error">{error}</p>}
      </section>

      <section className="panel network-facts">
        <div className="network-facts-col">
          <strong>Réseaux Docker</strong>
          {!net.measured && <span className="muted">L’agent de ce node ne décrit pas encore ses réseaux : il le fera après sa mise à jour.</span>}
          {net.networks.map((n) => (
            <div key={n.network} className="net-line">
              <span className="mono">{n.network}</span>
              <span className="muted">
                {n.subnet ? `${n.subnet} · ` : ''}IP {n.ip || '—'}
                {n.aliases?.length ? ` · nom ${n.aliases.join(', ')}` : ''}
              </span>
            </div>
          ))}
          {net.measured && (net.neighbors.length > 0 || net.others > 0) && (
            <>
              <span className="chips-row">
                {net.neighbors.map((n) => (
                  <span key={n} className="chip">
                    {n}
                  </span>
                ))}
                {net.others > 0 && <span className="chip muted">+ {net.others} autre{net.others > 1 ? 's' : ''}</span>}
              </span>
              <small className="muted">
                Ils peuvent joindre {app.name} par <span className="mono">{alias}{net.listening[0] ? `:${net.listening[0]}` : ''}</span> : c’est possible, pas
                forcément utilisé.
              </small>
            </>
          )}
        </div>
        <div className="network-facts-col">
          <strong>Ports</strong>
          <dl className="kv">
            <dt>Écoute réelle</dt>
            <dd className="mono">{net.listening.length ? net.listening.join(', ') : '—'}</dd>
            {net.routePort !== undefined && (
              <>
                <dt>Route Traefik</dt>
                <dd className={`mono ${net.listening.length && !net.listening.includes(net.routePort) ? 'state-down' : ''}`}>{net.routePort}</dd>
              </>
            )}
            <dt>Publié sur le node</dt>
            <dd className="mono">{net.published.length ? net.published.map((p) => `${p.hostPort}→${p.containerPort}`).join(' · ') : 'aucun (via Traefik)'}</dd>
          </dl>
        </div>
      </section>
    </div>
  )
}

// --- Nodes › Topologie and Tableau ---

export function useTopology() {
  const [topo, setTopo] = useState<Topology | null>(null)
  const [error, setError] = useState('')
  const load = () => api.topology().then(
    (t) => {
      setTopo(t)
      setError('')
    },
    (err) => setError(errorMessage(err)),
  )
  useEffect(() => {
    load()
    const t = setInterval(load, POLL_MS)
    return () => clearInterval(t)
  }, [])
  return { topo, error, reload: load }
}

const roleLabels: Record<TopoContainer['role'], string> = {
  app: 'app',
  sandbox: 'sandbox',
  traefik: 'Traefik',
  server: 'Forgeyard',
  agent: 'agent',
  external: 'externe',
}

/** What a container offers, in a line: its route, its published ports, or what it listens on. */
function containerLine(c: TopoContainer): string {
  if (c.role === 'sandbox') return 'sandbox · SSH'
  if (c.url) {
    const ok = !c.listening.length || c.listening.includes(c.routePort ?? 0)
    return `:${c.routePort}${ok ? '' : ' ✕'} ← ${c.url.replace('https://', '')}`
  }
  if (c.published.length) return c.published.map((p) => (p.hostPort === p.containerPort ? `:${p.hostPort}` : `${p.hostPort}→${p.containerPort}`)).join(' ')
  if (c.listening.length) return `écoute ${c.listening.join(', ')}`
  return c.image
}

function containerTone(c: TopoContainer): string {
  if (c.issue?.level === 'error' || (c.state !== 'running' && c.state !== 'created')) return 'down'
  if (c.issue) return 'warn'
  return 'up'
}

function nodeEntry(n: TopoNode, nodes: TopoNode[]): string {
  if (n.relayedBy) return `via ${nodes.find((x) => x.id === n.relayedBy)?.name ?? 'la machine de Forgeyard'} · :${n.httpPort}`
  if (n.ingressMode === 'traefik') return 'Traefik :80 :443'
  return `derrière ton proxy · :${n.httpPort}`
}

/** Where a container sits in the topology: its node and its networks. */
function topoKey(n: TopoNode, c: TopoContainer) {
  return `${n.id}/${c.id}`
}

/** Nodes › Topologie: Internet and the box on the left, each node with its Traefik and its networks as zones. */
export function TopologyMap() {
  const { topo, error, reload } = useTopology()
  const [selected, setSelected] = useState('')
  const [flows, setFlows] = useState(false)
  const [externals, setExternals] = useState(true)
  const [internals, setInternals] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const [lines, setLines] = useState<{ d: string; tone: string }[]>([])

  const nodes = topo?.nodes ?? []
  const find = (key: string) => {
    for (const n of nodes) for (const c of n.containers) if (topoKey(n, c) === key) return { n, c }
    return null
  }
  const sel = find(selected)
  const visible = (c: TopoContainer) =>
    (externals || c.role !== 'external') && (internals || (c.role !== 'server' && c.role !== 'agent'))

  // The links a request follows to an app: Internet → box → (relay) → its node's Traefik → the app.
  function pathOf(n: TopoNode, c: TopoContainer): [string, string][] {
    if (!c.url) return []
    const traefik = (node: TopoNode) => {
      const t = node.containers.find((x) => x.role === 'traefik')
      return t ? `c:${topoKey(node, t)}` : `n:${node.id}`
    }
    const hops: string[] = ['internet', 'entry']
    if (n.relayedBy) {
      const front = nodes.find((x) => x.id === n.relayedBy)
      if (front) hops.push(traefik(front))
    }
    hops.push(traefik(n), `c:${topoKey(n, c)}`)
    return hops.slice(1).map((h, i) => [hops[i], h])
  }

  // Lines are drawn over the layout, from the measured positions of what they link.
  useLayoutEffect(() => {
    const root = box.current
    if (!root) return
    const draw = () => {
      const links: { from: string; to: string; tone: string }[] = []
      for (const n of nodes)
        for (const c of n.containers) {
          const key = topoKey(n, c)
          if (!visible(c) || (!flows && key !== selected)) continue
          const tone = containerTone(c)
          for (const [from, to] of pathOf(n, c)) links.push({ from, to, tone: to === `c:${key}` ? tone : 'up' })
        }
      const base = root.getBoundingClientRect()
      const at = (k: string) => root.querySelector<HTMLElement>(`[data-topo="${CSS.escape(k)}"]`)?.getBoundingClientRect()
      const seen = new Set<string>()
      const out: { d: string; tone: string }[] = []
      for (const l of links) {
        const id = `${l.from}>${l.to}`
        if (seen.has(id)) continue
        seen.add(id)
        const a = at(l.from)
        const b = at(l.to)
        if (!a || !b) continue
        // Side by side: from the right edge to the left one. Below: from the bottom to the top.
        if (b.left >= a.right - 4) {
          const x1 = a.right - base.left
          const y1 = a.top + a.height / 2 - base.top
          const x2 = b.left - base.left
          const y2 = b.top + b.height / 2 - base.top
          const mx = (x1 + x2) / 2
          out.push({ d: `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`, tone: l.tone })
        } else {
          const x1 = a.left + a.width / 2 - base.left
          const y1 = a.bottom - base.top
          const x2 = b.left + b.width / 2 - base.left
          const y2 = b.top - base.top
          const my = (y1 + y2) / 2
          out.push({ d: `M${x1},${y1} C${x1},${my} ${x2},${my} ${x2},${y2}`, tone: l.tone })
        }
      }
      setLines(out)
    }
    draw()
    const ro = new ResizeObserver(draw)
    ro.observe(root)
    return () => ro.disconnect()
  }, [topo, selected, flows, externals, internals])

  if (!topo) return error ? <p className="error">{error}</p> : <div className="empty-state">Lecture de l’infrastructure…</div>
  const local = nodes.find((n) => n.isLocal)
  const issues = nodes.flatMap((n) => n.containers.filter((c) => c.issue && visible(c)))

  return (
    <div className="topo">
      <div className="topo-toolbar">
        <label className="chip-toggle">
          <input type="checkbox" checked={flows} onChange={(e) => setFlows(e.target.checked)} /> Flux
        </label>
        <label className="chip-toggle">
          <input type="checkbox" checked={externals} onChange={(e) => setExternals(e.target.checked)} /> Conteneurs externes
        </label>
        <label className="chip-toggle">
          <input type="checkbox" checked={internals} onChange={(e) => setInternals(e.target.checked)} /> Forgeyard lui-même
        </label>
        <span className="muted topo-hint">Cliquez un conteneur : son chemin s’allume.</span>
        {issues.length > 0 && (
          <span className="topo-issues">
            {issues.length} problème{issues.length > 1 ? 's' : ''}
          </span>
        )}
      </div>
      <div className="topo-body">
        <div className="panel topo-canvas" ref={box} onClick={() => setSelected('')}>
          <svg className="topo-lines" aria-hidden="true">
            <defs>
              {['up', 'warn', 'down'].map((t) => (
                <marker key={t} id={`topo-arrow-${t}`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
                  <path d="M0 0L10 5L0 10z" className={`topo-arrow-${t}`} />
                </marker>
              ))}
            </defs>
            {lines.map((l, i) => (
              <path key={i} d={l.d} className={`topo-line topo-line-${l.tone}`} markerEnd={`url(#topo-arrow-${l.tone})`} />
            ))}
          </svg>
          <div className="topo-entry">
            <span className="topo-col-label">Internet</span>
            <div className="topo-card topo-internet" data-topo="internet">
              <StepIcon k="visitor" />
              <strong>Visiteurs</strong>
              <span className="muted">{topo.domain ? `*.${topo.domain}` : 'pas de domaine'}</span>
            </div>
            <span className="topo-col-label">Entrée</span>
            <div className="topo-card" data-topo="entry">
              <StepIcon k="entry" />
              <strong>Box {topo.publicIp ?? ''}</strong>
              <span className="mono muted">:80 :443 → {local?.name ?? 'Forgeyard'}</span>
              {topo.sshPort ? <span className="mono muted">:{topo.sshPort} → SSH</span> : null}
            </div>
          </div>
          <div className="topo-nodes">
            {nodes.map((n) => {
              const traefik = n.containers.find((c) => c.role === 'traefik')
              const shown = n.containers.filter((c) => c.role !== 'traefik' && visible(c))
              const zones = n.networks
                .map((net) => ({ net, members: shown.filter((c) => c.endpoints.some((e) => e.network === net.name)) }))
                .filter((z) => z.members.length > 0)
              const loose = shown.filter((c) => !c.endpoints.length)
              return (
                <section key={n.id} className={`topo-node ${n.state === 'offline' ? 'topo-node-off' : ''}`}>
                  <header className="topo-node-head">
                    <span className={`dot ${n.state === 'online' ? 'dot-up' : 'dot-down'}`} />
                    <strong>{n.name}</strong>
                    <span className="muted">
                      {n.localIp ?? ''} · {nodeEntry(n, nodes)}
                    </span>
                  </header>
                  {!n.measured && <p className="muted">{n.state === 'offline' ? 'Hors ligne.' : 'L’agent de ce node ne décrit pas encore ses réseaux : il le fera après sa mise à jour.'}</p>}
                  {traefik && (
                    <ContainerChip
                      c={traefik}
                      tkey={topoKey(n, traefik)}
                      selected={selected}
                      onSelect={setSelected}
                      dim={!!sel && !(sel.c.url && (sel.n.id === n.id || sel.n.relayedBy === n.id)) && selected !== topoKey(n, traefik)}
                    />
                  )}
                  {zones.map(({ net, members }) => (
                    <div key={net.name} className="topo-zone">
                      <span className="topo-zone-label mono">
                        {net.name}
                        {net.subnet ? ` · ${net.subnet}` : ''}
                        {net.internal ? ' · interne' : ''}
                      </span>
                      <div className="topo-chips">
                        {members.map((c) => (
                          <ContainerChip
                            key={c.id}
                            c={c}
                            tkey={topoKey(n, c)}
                            selected={selected}
                            onSelect={setSelected}
                            dim={!!sel && selected !== topoKey(n, c)}
                          />
                        ))}
                      </div>
                    </div>
                  ))}
                  {loose.length > 0 && (
                    <div className="topo-zone topo-zone-host">
                      <span className="topo-zone-label mono">réseau de l’hôte</span>
                      <div className="topo-chips">
                        {loose.map((c) => (
                          <ContainerChip key={c.id} c={c} tkey={topoKey(n, c)} selected={selected} onSelect={setSelected} dim={!!sel && selected !== topoKey(n, c)} />
                        ))}
                      </div>
                    </div>
                  )}
                </section>
              )
            })}
          </div>
        </div>
        <Inspector sel={sel} nodes={nodes} onChange={reload} />
      </div>
      <p className="muted topo-legend">
        Un cadre = un réseau Docker : ceux qui y sont peuvent se joindre, ce qui ne veut pas dire qu’ils le font. Trait plein = route configurée.
      </p>
    </div>
  )
}

function ContainerChip({
  c,
  tkey,
  selected,
  onSelect,
  dim,
}: {
  c: TopoContainer
  tkey: string
  selected: string
  onSelect: (k: string) => void
  dim: boolean
}) {
  const tone = containerTone(c)
  return (
    <button
      type="button"
      className={`topo-chip topo-chip-${tone} ${selected === tkey ? 'on' : ''} ${dim ? 'dim' : ''} ${c.role === 'traefik' ? 'topo-chip-traefik' : ''}`}
      data-topo={`c:${tkey}`}
      onClick={(e) => {
        e.stopPropagation()
        onSelect(selected === tkey ? '' : tkey)
      }}
    >
      <span className="topo-chip-head">
        <span className={`dot dot-${tone}`} />
        <strong>{c.role === 'traefik' ? 'Traefik' : c.name}</strong>
        {c.role !== 'app' && c.role !== 'traefik' && <span className="topo-role">{roleLabels[c.role]}</span>}
        {c.issue && <span className="topo-bang">!</span>}
      </span>
      <span className="mono topo-chip-line">{containerLine(c)}</span>
    </button>
  )
}

/** The selected container: where it sits, what it offers, and its problem with its fix. */
function Inspector({ sel, nodes, onChange }: { sel: { n: TopoNode; c: TopoContainer } | null; nodes: TopoNode[]; onChange: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  if (!sel) {
    const problems = nodes.flatMap((n) => n.containers.filter((c) => c.issue).map((c) => ({ n, c })))
    return (
      <aside className="panel topo-inspector">
        <strong>Inspecter</strong>
        <span className="muted">Cliquez un conteneur pour voir ses réseaux, ses ports et le chemin de ses visites.</span>
        {problems.length > 0 && (
          <>
            <strong className="topo-inspector-sub">À corriger</strong>
            {problems.map(({ n, c }) => (
              <div key={n.id + c.id} className="topo-problem">
                <strong>{c.name}</strong> <span className="muted">· {n.name}</span>
                <span>{c.issue!.message}</span>
              </div>
            ))}
          </>
        )}
      </aside>
    )
  }
  const { n, c } = sel
  return (
    <aside className="panel topo-inspector">
      <div className="topo-inspector-head">
        <span className={`dot dot-${containerTone(c)}`} />
        <strong>{c.role === 'traefik' ? `Traefik · ${n.name}` : c.name}</strong>
        <span className="topo-role">{roleLabels[c.role]}</span>
      </div>
      <span className="muted mono topo-image">{c.image}</span>
      {c.issue && (
        <Issue
          d={c.issue}
          busy={busy}
          onFix={
            c.appId
              ? async (port) => {
                  setBusy(true)
                  setError('')
                  try {
                    await setAppPort(c.appId!, port)
                    onChange()
                  } catch (err) {
                    setError(errorMessage(err))
                  } finally {
                    setBusy(false)
                  }
                }
              : undefined
          }
        />
      )}
      {error && <p className="error">{error}</p>}
      <dl className="kv">
        <dt>Node</dt>
        <dd>{n.name}</dd>
        {c.ownerName && (
          <>
            <dt>Propriétaire</dt>
            <dd>{c.ownerName}</dd>
          </>
        )}
        {c.composeProject && (
          <>
            <dt>Compose</dt>
            <dd className="mono">{c.composeProject}</dd>
          </>
        )}
        <dt>Écoute</dt>
        <dd className="mono">{c.listening.length ? c.listening.join(', ') : '—'}</dd>
        <dt>Publié</dt>
        <dd className="mono">{c.published.length ? c.published.map((p) => `${p.hostPort}→${p.containerPort}/${p.protocol}`).join(' ') : '—'}</dd>
        {c.url && (
          <>
            <dt>Route</dt>
            <dd className="mono">
              {c.url.replace('https://', '')} → :{c.routePort}
            </dd>
          </>
        )}
      </dl>
      {c.endpoints.length > 0 && (
        <>
          <strong className="topo-inspector-sub">Réseaux</strong>
          {c.endpoints.map((e) => (
            <div key={e.network} className="net-line">
              <span className="mono">{e.network}</span>
              <span className="muted mono">
                {e.ip || '—'}
                {e.aliases?.length ? ` · ${e.aliases.join(', ')}` : ''}
              </span>
            </div>
          ))}
        </>
      )}
      {c.steps && c.steps.length > 0 && (
        <>
          <strong className="topo-inspector-sub">Chemin d’une visite</strong>
          <ol className="path-list">
            {c.steps.map((s, i) => (
              <li key={i} className={`path-${s.state}`}>
                <span className={`dot ${stateTone[s.state] ? `dot-${stateTone[s.state]}` : ''}`} />
                <span>
                  <strong>{s.title}</strong> <span className="muted">{s.note}</span>
                </span>
              </li>
            ))}
          </ol>
        </>
      )}
      {c.appId && (
        <a className="btn btn-block" href={`#/apps/${c.appId}`}>
          Ouvrir l’app
        </a>
      )}
    </aside>
  )
}

type TableFilter = { query: string; problemsFirst: boolean; externals: boolean }

/** Nodes › Tableau: every container on a line, with its networks, ports and route; it scales to many. */
export function TopologyTable() {
  const { topo, error } = useTopology()
  const [f, setF] = useState<TableFilter>({ query: '', problemsFirst: true, externals: true })
  if (!topo) return error ? <p className="error">{error}</p> : <div className="empty-state">Lecture de l’infrastructure…</div>
  const q = f.query.trim().toLowerCase()
  const groups = topo.nodes.map((n) => {
    let rows = n.containers.filter(
      (c) =>
        (f.externals || c.role !== 'external') &&
        (!q ||
          [c.name, c.image, c.url ?? '', c.composeProject ?? '', ...c.endpoints.flatMap((e) => [e.network, e.ip ?? '', ...(e.aliases ?? [])])].some((s) =>
            s.toLowerCase().includes(q),
          )),
    )
    if (f.problemsFirst) rows = [...rows].sort((a, b) => Number(!!b.issue) - Number(!!a.issue))
    return { n, rows }
  })
  const issues = topo.nodes.flatMap((n) => n.containers.filter((c) => c.issue))
  const networks = new Set(topo.nodes.flatMap((n) => n.networks.map((x) => `${n.id}/${x.name}`))).size
  const containers = topo.nodes.reduce((s, n) => s + n.containers.length, 0)

  return (
    <div className="topo">
      <div className="topo-toolbar">
        <input className="search-input" placeholder="Filtrer : nom, image, IP, réseau…" value={f.query} onChange={(e) => setF({ ...f, query: e.target.value })} />
        <label className="chip-toggle">
          <input type="checkbox" checked={f.problemsFirst} onChange={(e) => setF({ ...f, problemsFirst: e.target.checked })} /> Problèmes d’abord
        </label>
        <label className="chip-toggle">
          <input type="checkbox" checked={f.externals} onChange={(e) => setF({ ...f, externals: e.target.checked })} /> Conteneurs externes
        </label>
        <span className="muted topo-hint">
          {networks} réseau{networks > 1 ? 'x' : ''} · {containers} conteneurs · {issues.length} problème{issues.length > 1 ? 's' : ''}
        </span>
      </div>
      <div className="panel topo-table-wrap">
        <table className="topo-table">
          <thead>
            <tr>
              <th>Conteneur</th>
              <th>Réseaux et IP</th>
              <th>Écoute</th>
              <th>Publié sur le node</th>
              <th>Route Traefik</th>
              <th>Problème</th>
            </tr>
          </thead>
          <tbody>
            {groups.map(({ n, rows }) => [
              <tr key={`n${n.id}`} className="topo-table-node">
                <td colSpan={6}>
                  <span className={`dot ${n.state === 'online' ? 'dot-up' : 'dot-down'}`} /> {n.name}
                  <span className="muted">
                    {' '}
                    · {n.localIp ?? ''} · {nodeEntry(n, topo.nodes)}
                    {!n.measured && n.state === 'online' ? ' · agent à mettre à jour' : ''}
                  </span>
                </td>
              </tr>,
              ...rows.map((c) => (
                <tr key={n.id + c.id} className={c.issue ? 'topo-row-issue' : ''}>
                  <td>
                    <span className={`dot dot-${containerTone(c)}`} />{' '}
                    {c.appId ? <a href={`#/apps/${c.appId}`}>{c.name}</a> : <strong>{c.role === 'traefik' ? 'Traefik' : c.name}</strong>}{' '}
                    <span className="topo-role">{roleLabels[c.role]}</span>
                  </td>
                  <td>
                    <span className="chips-row">
                      {c.endpoints.length
                        ? c.endpoints.map((e) => (
                            <span key={e.network} className="net-chip mono" title={e.aliases?.length ? `noms : ${e.aliases.join(', ')}` : undefined}>
                              {e.network} <b>{e.ip || '—'}</b>
                            </span>
                          ))
                        : '—'}
                    </span>
                  </td>
                  <td className={`mono ${c.url && c.listening.length && !c.listening.includes(c.routePort ?? 0) ? 'state-down' : ''}`}>
                    {c.listening.length ? c.listening.join(', ') : '—'}
                  </td>
                  <td className="mono">{c.published.length ? c.published.map((p) => `${p.hostPort}→${p.containerPort}`).join(' · ') : '—'}</td>
                  <td className="mono">{c.url ? `${c.url.replace('https://', '')} → ${c.routePort}` : c.role === 'sandbox' ? 'SSH' : '—'}</td>
                  <td className={c.issue ? 'state-down' : 'muted'}>{c.issue ? c.issue.message : ''}</td>
                </tr>
              )),
            ])}
          </tbody>
        </table>
      </div>
      <p className="muted topo-legend">Deux conteneurs sur le même réseau peuvent se joindre par leur nom ; le tableau ne dit pas s’ils le font.</p>
    </div>
  )
}

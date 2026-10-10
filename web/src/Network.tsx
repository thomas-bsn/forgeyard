import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { api, errorMessage, type App, type AppNetwork, type Diagnosis, type PathStep, type ProbeResult, type Topology, type TopoContainer, type TopoNode } from './api'
import { AppLogo, since } from './ui'

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

/** Where a container sits in the topology: its node and its id. */
function topoKey(n: TopoNode, c: TopoContainer) {
  return `${n.id}/${c.id}`
}

/** The short name of an image: grafana/grafana:11 → grafana. */
function shortImage(image: string): string {
  const last = image.split('/').pop() ?? image
  return last.split(/[:@]/)[0]
}

/** What a container offers, in a line: its image and route, its published ports, or its image. */
function chipLine(c: TopoContainer): string {
  if (c.role === 'sandbox') return 'sandbox · SSH'
  if (c.role === 'traefik') return ''
  if (c.url) {
    if (c.listening.length && !c.listening.includes(c.routePort ?? 0)) return `route :${c.routePort} · écoute :${c.listening.join(', :')}`
    return `${shortImage(c.image)} :${c.routePort}`
  }
  if (c.published.length) return c.published.map((p) => (p.hostPort === p.containerPort ? `${p.hostPort}` : `${p.hostPort}→${p.containerPort}`)).join(', ')
  if (c.listening.length) return `${shortImage(c.image)} :${c.listening.join(', :')}`
  return shortImage(c.image)
}

function traefikLine(n: TopoNode, c: TopoContainer): string {
  const published = c.published.map((p) => `:${p.hostPort}`)
  if (n.ingressMode === 'traefik') return `${published.length ? published.join(' ') : ':80 :443'} publiés`
  return `:${n.httpPort} (LAN)`
}

function nodeEntry(n: TopoNode, nodes: TopoNode[]): string {
  if (n.relayedBy) return `via ${nodes.find((x) => x.id === n.relayedBy)?.name ?? 'la machine de Forgeyard'}`
  if (n.ingressMode === 'traefik') return 'Traefik :80/:443'
  return `derrière ton proxy · :${n.httpPort}`
}

function containerTone(c: TopoContainer): string {
  if (c.issue?.level === 'error' || (c.state !== 'running' && c.state !== 'created')) return 'down'
  if (c.issue) return 'warn'
  return 'up'
}

type Link = { from: string; to: string; kind: 'entry' | 'relay' | 'route' | 'path' | 'possible'; tone: string; label?: string }

/** Nodes › Topologie: Internet, the box, then each node with its Traefik and its Docker networks as zones. */
export function TopologyMap() {
  const { topo, error, reload } = useTopology()
  const [selected, setSelected] = useState('')
  const [routes, setRoutes] = useState(false)
  const [zones, setZones] = useState(true)
  const [possible, setPossible] = useState(false)
  const [externals, setExternals] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const [lines, setLines] = useState<{ d: string; kind: Link['kind']; tone: string; lit: boolean; label?: string; lx: number; ly: number; vertical?: boolean }[]>([])

  const nodes = topo?.nodes ?? []
  const find = (key: string) => {
    for (const n of nodes) for (const c of n.containers) if (topoKey(n, c) === key) return { n, c }
    return null
  }
  const sel = find(selected)
  const visible = (c: TopoContainer) => (externals || c.role !== 'external') && c.role !== 'server' && c.role !== 'agent'
  const traefikOf = (n: TopoNode) => {
    const t = n.containers.find((x) => x.role === 'traefik')
    return t ? `c:${topoKey(n, t)}` : `n:${n.id}`
  }
  // Nodes the box sends visits to: Forgeyard's machine, and the nodes with their own Traefik and IP.
  const fronts = nodes.filter((n) => n.isLocal || (n.ingressMode === 'traefik' && !n.relayedBy))
  // What the selected container can reach: the running ones sharing one of its networks.
  const reachable = new Set<string>()
  if (sel)
    for (const c of sel.n.containers)
      if (c.id !== sel.c.id && c.state === 'running' && c.endpoints.some((e) => sel.c.endpoints.some((s) => s.network === e.network))) reachable.add(topoKey(sel.n, c))
  // What lights up with the selection: its path.
  const onPath = new Set<string>()
  if (sel?.c.url) {
    onPath.add('internet').add('entry').add(traefikOf(sel.n)).add(`c:${selected}`)
    const front = nodes.find((x) => x.id === sel.n.relayedBy)
    if (front) onPath.add(traefikOf(front))
  }

  function links(): Link[] {
    const out: Link[] = [{ from: 'internet', to: 'entry', kind: 'entry', tone: 'up' }]
    for (const n of fronts) out.push({ from: 'entry', to: traefikOf(n), kind: 'entry', tone: 'up' })
    for (const n of nodes) {
      const front = nodes.find((x) => x.id === n.relayedBy)
      if (front) out.push({ from: traefikOf(front), to: traefikOf(n), kind: 'relay', tone: 'relay', label: 'relais LAN' })
      for (const c of n.containers) {
        const key = topoKey(n, c)
        if (!c.url || !visible(c)) continue
        if (routes || key === selected) out.push({ from: traefikOf(n), to: `c:${key}`, kind: key === selected ? 'path' : 'route', tone: containerTone(c) })
      }
    }
    if (possible && sel) for (const k of reachable) out.push({ from: `c:${selected}`, to: `c:${k}`, kind: 'possible', tone: 'muted' })
    return out
  }

  // Lines are drawn over the layout, from the measured positions of what they link.
  useLayoutEffect(() => {
    const root = box.current
    if (!root) return
    const draw = () => {
      const base = root.getBoundingClientRect()
      const el = (k: string) => root.querySelector<HTMLElement>(`[data-topo="${CSS.escape(k)}"]`)
      const at = (k: string) => el(k)?.getBoundingClientRect()
      const out: typeof lines = []
      for (const l of links()) {
        const a = at(l.from)
        const b = at(l.to)
        if (!a || !b) continue
        const lit = onPath.has(l.from) && onPath.has(l.to)
        const rel = (r: DOMRect) => ({ l: r.left - base.left, r: r.right - base.left, t: r.top - base.top, b: r.bottom - base.top, cx: r.left + r.width / 2 - base.left, cy: r.top + r.height / 2 - base.top })
        const A = rel(a)
        const B = rel(b)
        let d: string
        let lx: number
        let ly: number
        if (B.l >= A.r - 4) {
          // Side by side: from the right edge to the left one.
          const mx = (A.r + B.l) / 2
          d = `M${A.r},${A.cy} C${mx},${A.cy} ${mx},${B.cy} ${B.l},${B.cy}`
          lx = mx
          ly = (A.cy + B.cy) / 2
        } else if (l.kind === 'relay') {
          // From one node's Traefik to another's, below it: around the node cards, on their right.
          const cards = [el(l.from), el(l.to)].map((e) => e?.closest('.topo-node')?.getBoundingClientRect().right ?? 0)
          const x = Math.max(...cards) - base.left + 22
          const r = 10
          d = `M${A.r},${A.cy} H${x - r} Q${x},${A.cy} ${x},${A.cy + r} V${B.cy - r} Q${x},${B.cy} ${x - r},${B.cy} H${B.r}`
          out.push({ d, kind: l.kind, tone: l.tone, lit, label: l.label, lx: x + 13, ly: (A.cy + B.cy) / 2, vertical: true })
          continue
        } else {
          // Below: from the bottom to the top.
          const my = (A.b + B.t) / 2
          d = `M${A.cx},${A.b} C${A.cx},${my} ${B.cx},${my} ${B.cx},${B.t}`
          lx = (A.cx + B.cx) / 2
          ly = my
        }
        out.push({ d, kind: l.kind, tone: l.tone, lit, label: l.label, lx, ly })
      }
      setLines(out)
    }
    draw()
    const ro = new ResizeObserver(draw)
    ro.observe(root)
    return () => ro.disconnect()
  }, [topo, selected, routes, zones, possible, externals])

  if (!topo) return error ? <p className="error">{error}</p> : <div className="empty-state">Lecture de l’infrastructure…</div>
  const local = nodes.find((n) => n.isLocal)
  const issues = nodes.flatMap((n) => n.containers.filter((c) => c.issue && visible(c)))
  const dim = (key: string) => !!sel && key !== `c:${selected}` && !onPath.has(key) && !(possible && reachable.has(key.slice(2)))

  return (
    <div className="topo">
      <div className="topo-toolbar">
        <span className="muted topo-hint">Cliquez un conteneur : il s’inspecte à droite et son chemin s’allume.</span>
        {issues.length > 0 && (
          <span className="topo-issues">
            {issues.length} problème{issues.length > 1 ? 's' : ''}
          </span>
        )}
        <span className="topo-filters">
          <FilterChip on={routes} set={setRoutes} label="Routes" />
          <FilterChip on={zones} set={setZones} label="Réseaux" />
          <FilterChip on={possible} set={setPossible} label="Liens possibles" />
          <FilterChip on={externals} set={setExternals} label="Conteneurs externes" />
        </span>
      </div>
      <div className="topo-body">
        <div className="panel topo-canvas" ref={box} onClick={() => setSelected('')}>
          <svg className="topo-lines" aria-hidden="true">
            <defs>
              {['up', 'warn', 'down', 'relay', 'muted'].map((t) => (
                <marker key={t} id={`topo-arrow-${t}`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
                  <path d="M0 0L10 5L0 10z" className={`topo-arrow-${t}`} />
                </marker>
              ))}
            </defs>
            {lines.map((l, i) => (
              <g key={i}>
                <path
                  d={l.d}
                  className={`topo-line topo-line-${l.kind} topo-line-${l.tone} ${l.lit ? 'topo-line-lit' : ''} ${sel && !l.lit && l.kind !== 'possible' ? 'topo-line-faded' : ''}`}
                  markerEnd={l.kind === 'possible' ? undefined : `url(#topo-arrow-${l.lit && l.kind === 'entry' ? 'up' : l.tone})`}
                />
                {l.label && (
                  <text
                    x={l.lx}
                    y={l.ly}
                    className="topo-line-label"
                    textAnchor="middle"
                    transform={l.vertical ? `rotate(90 ${l.lx} ${l.ly})` : undefined}
                  >
                    {l.label}
                  </text>
                )}
              </g>
            ))}
          </svg>
          <span className="topo-col-label topo-head-internet">Internet</span>
          <span className="topo-col-label topo-head-entry">Entrée</span>
          <span className="topo-col-label topo-head-nodes">Nodes · réseaux · conteneurs</span>
          <div className="topo-col-internet">
            <div className={`topo-globe ${dim('internet') ? 'dim' : ''}`} data-topo="internet">
              <StepIcon k="visitor" />
              <span>visiteurs</span>
            </div>
          </div>
          <div className="topo-col-entry">
            <div className={`topo-box ${dim('entry') ? 'dim' : ''}`} data-topo="entry">
              <strong>Box{topo.publicIp ? ` · ${topo.publicIp}` : ''}</strong>
              <span className="mono">:80 :443 → {local?.name ?? 'Forgeyard'}</span>
              {topo.sshPort ? (
                <span className="mono">
                  :{topo.sshPort} → {local?.name ?? 'Forgeyard'} (SSH)
                </span>
              ) : null}
              <span className="muted">{topo.domain ? `DNS *.${topo.domain}` : 'pas de domaine'}</span>
            </div>
          </div>
          <div className="topo-nodes">
            {nodes.map((n) => {
              const traefik = n.containers.find((c) => c.role === 'traefik')
              const shown = n.containers.filter((c) => c.role !== 'traefik' && visible(c))
              const ordered = [...n.networks].sort((a, b) => Number(b.name === 'forgeyard') - Number(a.name === 'forgeyard'))
              const groups = zones
                ? ordered.map((net) => ({ net, members: shown.filter((c) => c.endpoints.some((e) => e.network === net.name)) })).filter((z) => z.members.length > 0)
                : [{ net: null, members: shown }]
              const loose = zones ? shown.filter((c) => !c.endpoints.length) : []
              const chip = (c: TopoContainer) => (
                <ContainerChip key={c.id} c={c} n={n} selected={selected} onSelect={setSelected} dim={dim(`c:${topoKey(n, c)}`)} />
              )
              return (
                <section key={n.id} className={`topo-node ${n.state === 'offline' ? 'topo-node-off' : ''}`}>
                  <header className="topo-node-head">
                    <strong>
                      {n.name}
                      {n.isLocal ? ' · Forgeyard' : ''}
                    </strong>
                    <span className="muted">
                      {n.localIp ? `${n.localIp} · ` : ''}
                      {nodeEntry(n, nodes)}
                      {n.state === 'offline' ? ' · hors ligne' : ''}
                    </span>
                  </header>
                  {!n.measured && (
                    <p className="muted topo-node-note">
                      {n.state === 'offline' ? 'Hors ligne : sa dernière description n’est pas connue.' : 'L’agent de ce node ne décrit pas encore ses réseaux : il le fera après sa mise à jour.'}
                    </p>
                  )}
                  {traefik && (
                    <button
                      type="button"
                      className={`topo-traefik ${selected === topoKey(n, traefik) ? 'on' : ''} ${dim(`c:${topoKey(n, traefik)}`) ? 'dim' : ''} ${traefik.issue ? 'topo-chip-down' : ''}`}
                      data-topo={`c:${topoKey(n, traefik)}`}
                      onClick={(e) => {
                        e.stopPropagation()
                        setSelected(selected === topoKey(n, traefik) ? '' : topoKey(n, traefik))
                      }}
                    >
                      <span className="topo-traefik-icon" aria-hidden="true">
                        <StepIcon k="traefik" />
                      </span>
                      <span>
                        <strong>traefik</strong>
                        <span className="mono">{traefikLine(n, traefik)}</span>
                      </span>
                    </button>
                  )}
                  {(groups.length > 0 || loose.length > 0) && (
                    <div className="topo-zones">
                      {groups.map(({ net, members }) =>
                        net ? (
                          <div key={net.name} className={`topo-zone ${net.name === 'forgeyard' ? '' : 'topo-zone-ext'}`}>
                            <span className="topo-zone-label mono">
                              {net.name === 'forgeyard' ? 'réseau forgeyard' : net.name}
                              {net.subnet ? ` · ${net.subnet}` : ''}
                              {net.internal ? ' · interne' : ''}
                            </span>
                            <div className="topo-chips">{members.map(chip)}</div>
                          </div>
                        ) : (
                          <div key="all" className="topo-chips topo-chips-flat">
                            {members.map(chip)}
                          </div>
                        ),
                      )}
                      {loose.length > 0 && (
                        <div className="topo-zone topo-zone-host">
                          <span className="topo-zone-label mono">réseau de l’hôte</span>
                          <div className="topo-chips">{loose.map(chip)}</div>
                        </div>
                      )}
                    </div>
                  )}
                </section>
              )
            })}
          </div>
        </div>
        <Inspector sel={sel} nodes={nodes} onChange={reload} />
      </div>
    </div>
  )
}

function FilterChip({ on, set, label }: { on: boolean; set: (v: boolean) => void; label: string }) {
  return (
    <button type="button" className={`chip ${on ? 'on' : ''}`} aria-pressed={on} onClick={() => set(!on)}>
      {label}
    </button>
  )
}

function ContainerChip({ c, n, selected, onSelect, dim }: { c: TopoContainer; n: TopoNode; selected: string; onSelect: (k: string) => void; dim: boolean }) {
  const tone = containerTone(c)
  const key = topoKey(n, c)
  return (
    <button
      type="button"
      className={`topo-chip topo-chip-${tone} ${c.issue ? 'topo-chip-issue' : ''} ${selected === key ? 'on' : ''} ${dim ? 'dim' : ''}`}
      data-topo={`c:${key}`}
      title={c.issue?.message}
      onClick={(e) => {
        e.stopPropagation()
        onSelect(selected === key ? '' : key)
      }}
    >
      {c.issue && (
        <span className="topo-bang" aria-label="problème">
          !
        </span>
      )}
      <span className="topo-chip-head">
        <span className={`dot dot-${tone}`} />
        <strong>{c.name}</strong>
      </span>
      <span className="mono topo-chip-line">{chipLine(c)}</span>
    </button>
  )
}

/** The selected container: who it is, its problem with its fix, and where it sits. */
function Inspector({ sel, nodes, onChange }: { sel: { n: TopoNode; c: TopoContainer } | null; nodes: TopoNode[]; onChange: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const legend = (
    <p className="muted topo-legend">
      <span className="legend-line legend-route" /> route configurée <span className="legend-line legend-relay" /> relais{' '}
      <span className="legend-line legend-down" /> en erreur
      <br />
      Liens « possibles » (même réseau) masqués par défaut : être sur un même réseau permet de se joindre, sans dire qu’on le fait.
    </p>
  )
  if (!sel) {
    const problems = nodes.flatMap((n) => n.containers.filter((c) => c.issue).map((c) => ({ n, c })))
    return (
      <aside className="panel topo-inspector">
        <strong>Inspecter</strong>
        <span className="muted">Cliquez un conteneur pour voir ses réseaux, ses ports et le chemin de ses visites.</span>
        {problems.map(({ n, c }) => (
          <div key={n.id + c.id} className="topo-problem">
            <strong>
              {c.role === 'traefik' ? 'Traefik' : c.name} <span className="muted">· {n.name}</span>
            </strong>
            <span>{c.issue!.message}</span>
          </div>
        ))}
        {legend}
      </aside>
    )
  }
  const { n, c } = sel
  const first = c.endpoints.find((e) => e.ip) ?? c.endpoints[0]
  return (
    <aside className="panel topo-inspector">
      <div className="topo-inspector-head">
        <AppLogo url={c.logoUrl} color={c.logoColor} name={c.role === 'traefik' ? 'Traefik' : c.name} size={38} />
        <span className="topo-inspector-title">
          <strong>{c.role === 'traefik' ? 'traefik' : c.name}</strong>
          <span className="muted">
            {n.name}
            {c.ownerName ? ` · ${c.ownerName}` : c.role !== 'app' ? ` · ${roleLabels[c.role]}` : ''}
          </span>
        </span>
      </div>
      {c.issue && (
        <div className={`topo-issue topo-issue-${c.issue.level}`}>
          <span>{c.issue.message}</span>
          {c.issue.fixPort && c.appId && (
            <button
              type="button"
              className="btn btn-primary btn-small"
              disabled={busy}
              onClick={async () => {
                setBusy(true)
                setError('')
                try {
                  await setAppPort(c.appId!, c.issue!.fixPort!)
                  onChange()
                } catch (err) {
                  setError(errorMessage(err))
                } finally {
                  setBusy(false)
                }
              }}
            >
              Utiliser {c.issue.fixPort}
            </button>
          )}
        </div>
      )}
      {error && <p className="error">{error}</p>}
      <dl className="kv">
        <dt>IP</dt>
        <dd className="mono">{first?.ip || '—'}</dd>
        <dt>Alias</dt>
        <dd className="mono">{first?.aliases?.length ? first.aliases.join(', ') : '—'}</dd>
        <dt>Réseaux</dt>
        <dd className="mono">{c.endpoints.length ? c.endpoints.map((e) => e.network).join(', ') : 'hôte'}</dd>
        <dt>Écoute</dt>
        <dd className="mono">{c.listening.length ? c.listening.join(', ') : '—'}</dd>
        <dt>Hôte</dt>
        <dd className="mono">{c.published.length ? c.published.map((p) => `${p.hostPort}→${p.containerPort}`).join(' ') : '—'}</dd>
        {c.url && (
          <>
            <dt>Route</dt>
            <dd className="mono">
              {c.url.replace('https://', '')} → :{c.routePort}
            </dd>
          </>
        )}
      </dl>
      {c.appId && (
        <>
          <a className="btn btn-block" href={`#/apps/${c.appId}`}>
            Ouvrir l’app
          </a>
          <a className="btn btn-block" href={`#/apps/${c.appId}/network`}>
            Voir le chemin complet
          </a>
        </>
      )}
      {legend}
    </aside>
  )
}

type TableFilter = { query: string; problemsFirst: boolean; externals: boolean }

/** Nodes › Tableau: every container on a line, with its networks, ports and route; it scales to many. */
export function TopologyTable() {
  const { topo, error } = useTopology()
  const [f, setF] = useState<TableFilter>({ query: '', problemsFirst: true, externals: false })
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

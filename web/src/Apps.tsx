import { lazy, Suspense, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { api, errorMessage, type App, type AppEvent, type AppInput, type AppState, type AppUsage, type Container, type Node, type NodeChoice } from './api'
import { AppLogo, AreaChart, CopyField, formatBytes, LOGO_COLORS, Modal, since, Switch, upFor } from './ui'
import { ImageCropper } from './Cropper'
import { AppNetworkTab } from './Network'
// xterm.js is large: it loads only when a terminal opens.
const Terminal = lazy(() => import('./Terminal'))

const POLL_MS = 3000
const USAGE_POLL_MS = 15000

const stateLabels: Record<AppState, { label: string; tone: 'up' | 'warn' | 'down' | '' }> = {
  pending: { label: 'En attente', tone: '' },
  pulling: { label: 'Téléchargement de l’image…', tone: 'warn' },
  building: { label: 'Construction de l’image…', tone: 'warn' },
  creating: { label: 'Démarrage…', tone: 'warn' },
  deploying: { label: 'Mise à jour sans coupure…', tone: 'warn' },
  running: { label: 'En ligne', tone: 'up' },
  restarting: { label: 'Redémarre en boucle', tone: 'down' },
  stopped: { label: 'Arrêtée', tone: '' },
  exited: { label: 'Plantée', tone: 'down' },
  error: { label: 'Erreur', tone: 'down' },
  'node-offline': { label: 'Node hors ligne', tone: 'down' },
}

function appTone(a: App): 'up' | 'warn' | 'down' | '' {
  if (a.crashSuspended && !a.running) return 'down'
  return stateLabels[a.state]?.tone ?? ''
}

function stateText(a: App): string {
  if (a.crashSuspended && !a.running) return 'Suspendue (crashs)'
  const base = stateLabels[a.state]?.label ?? a.state
  if (a.state === 'exited' || a.state === 'restarting') {
    if (a.oomKilled) return `${base} : manque de mémoire`
    if (a.exitCode) return `${base} (code ${a.exitCode})`
  }
  if (a.suspended && !a.running) return 'Suspendue'
  return base
}

const containerTones: Record<string, 'up' | 'warn' | 'down' | ''> = {
  running: 'up',
  restarting: 'down',
  paused: 'warn',
  created: '',
  exited: '',
  dead: 'down',
}

const containerLabels: Record<string, string> = {
  running: 'En ligne',
  restarting: 'Redémarre en boucle',
  paused: 'En pause',
  created: 'Créé',
  exited: 'Arrêté',
  dead: 'Mort',
}

function Dot({ tone }: { tone: string }) {
  return <span className={`dot ${tone ? `dot-${tone}` : ''}`} />
}

function useAppsData(admin: boolean) {
  const [apps, setApps] = useState<App[] | null>(null)
  const [nodes, setNodes] = useState<Node[]>([])
  const [containers, setContainers] = useState<Container[]>([])
  const [error, setError] = useState('')

  async function load() {
    try {
      const [a, n, c] = await Promise.all([api.apps(), admin ? api.nodes() : Promise.resolve([]), admin ? api.containers() : Promise.resolve([])])
      setApps(a)
      setNodes(n)
      setContainers(c)
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  useEffect(() => {
    load()
    const id = setInterval(load, POLL_MS)
    return () => clearInterval(id)
  }, [admin])

  return { apps, nodes, containers, error, load }
}

export default function Apps({
  admin,
  superadmin,
  layout,
  route,
  go,
}: {
  admin: boolean
  superadmin: boolean
  layout: AppsLayout
  route: string[]
  go: (path: string) => void
}) {
  const { apps, nodes, containers, error, load } = useAppsData(admin)

  if (route[0] === 'apps' && route[1]) {
    const app = apps?.find((a) => a.id === Number(route[1]))
    if (!apps) return null
    if (!app) return <NotFound what="Cette app n’existe plus." go={go} />
    return <AppDetail key={app.id + (route[2] ?? '')} app={app} admin={admin} nodes={nodes} onChange={load} go={go} openTab={route[2] === 'network' ? 'network' : undefined} />
  }
  if (route[0] === 'containers' && route[2]) {
    const c = containers.find((x) => x.nodeId === Number(route[1]) && x.id === route[2])
    if (!apps) return null
    if (!c) return <NotFound what="Ce conteneur n’existe plus, ou son node est hors ligne." go={go} />
    return <ContainerDetail container={c} superadmin={superadmin} onChange={load} go={go} />
  }
  return <AppList apps={apps} nodes={nodes} containers={containers} admin={admin} layout={layout} error={error} onChange={load} go={go} />
}

function NotFound({ what, go }: { what: string; go: (path: string) => void }) {
  return (
    <div className="empty-state">
      <strong>{what}</strong>
      <button type="button" className="btn" onClick={() => go('apps')}>
        ← Retour aux apps
      </button>
    </div>
  )
}

type Kind = 'all' | 'apps' | 'external'
type StateFilter = 'all' | 'up' | 'stopped' | 'problem'

/** An external container's exit code, read from Docker's status ("Exited (1) 2 hours ago"). */
function exitCode(c: Container): number {
  const m = /^Exited \((\d+)\)/.exec(c.status)
  return m ? Number(m[1]) : 0
}

function containerTone(c: Container): 'up' | 'warn' | 'down' | '' {
  if (c.state === 'exited' && exitCode(c) !== 0) return 'down'
  return containerTones[c.state] ?? ''
}

function containerText(c: Container): string {
  const code = exitCode(c)
  if (c.state === 'exited' && code !== 0) return `Planté (code ${code})`
  return containerLabels[c.state] ?? c.state
}

/** One entry of the list: a Forgeyard app or an external container, with what the filters need. */
type Item = { key: string; name: string; tone: string; group: StateFilter; external: boolean; app?: App; container?: Container }

function toItems(apps: App[], containers: Container[]): Item[] {
  const group = (tone: string, running: boolean): StateFilter => (tone === 'down' ? 'problem' : running ? 'up' : 'stopped')
  return [
    ...apps.map((a) => {
      const tone = appTone(a)
      return { key: `a${a.id}`, name: a.name, tone, group: group(tone, a.state === 'running' || tone === 'warn'), external: false, app: a }
    }),
    ...containers.map((c) => {
      const tone = containerTone(c)
      return { key: `c${c.nodeId}-${c.id}`, name: c.name, tone, group: group(tone, c.state === 'running'), external: true, container: c }
    }),
  ].sort((x, y) => {
    // Problems first, then what runs, then the rest; Forgeyard's apps before external containers.
    const rank = { problem: 0, up: 1, stopped: 2, all: 3 }
    return rank[x.group] - rank[y.group] || Number(x.external) - Number(y.external) || x.name.localeCompare(y.name)
  })
}

export type AppsLayout = 'sidebar' | 'nodes' | 'launcher'

/** The node picked in the Apps tab, remembered across visits; "all" shows every node. */
function useSelectedNode(): [string, (v: string) => void] {
  const [value, setValue] = useState(() => {
    try {
      return localStorage.getItem('appsNode') ?? 'all'
    } catch {
      return 'all'
    }
  })
  return [
    value,
    (v: string) => {
      setValue(v)
      try {
        localStorage.setItem('appsNode', v)
      } catch {
        // Private browsing: the choice just isn't remembered.
      }
    },
  ]
}

function AppList({
  apps,
  nodes,
  containers,
  admin,
  layout,
  error,
  onChange,
  go,
}: {
  apps: App[] | null
  nodes: Node[]
  containers: Container[]
  admin: boolean
  layout: AppsLayout
  error: string
  onChange: () => void
  go: (path: string) => void
}) {
  const [creating, setCreating] = useState(false)
  const [query, setQuery] = useState('')
  const [kind, setKind] = useState<Kind>('apps') // Forgeyard's apps first; external containers are one click away
  const [stateFilter, setStateFilter] = useState<StateFilter>('all')
  const [picked, setPicked] = useSelectedNode()

  const all = toItems(apps ?? [], containers)
  const nodeOf = (i: Item) => i.app?.nodeId ?? i.container?.nodeId
  const choices = nodes.filter((n) => n.state !== 'pending' || all.some((i) => nodeOf(i) === n.id))
  // Users never pick a node: they see their apps, wherever they run.
  const node = admin ? choices.find((n) => String(n.id) === picked) : undefined
  const inNode = all.filter((i) => !node || nodeOf(i) === node.id)

  const q = query.trim().toLowerCase()
  const matchesQuery = (i: Item) =>
    !q || [i.name, i.app?.image ?? i.container?.image ?? '', i.app?.ownerName ?? '', i.container?.composeProject ?? ''].some((f) => f.toLowerCase().includes(q))
  const byKind = inNode.filter((i) => kind === 'all' || (kind === 'external') === i.external)
  const shown = byKind.filter((i) => (stateFilter === 'all' || i.group === stateFilter) && matchesQuery(i))
  const count = (f: StateFilter) => byKind.filter((i) => i.group === f).length
  const problems = (id?: number) => all.filter((i) => (id === undefined || nodeOf(i) === id) && i.group === 'problem').length

  const newApp = (
    <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
      + Nouvelle app
    </button>
  )

  const filters = (
    <div className="filters">
      <label className="search">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="m20 20-3.5-3.5" />
        </svg>
        <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Rechercher" aria-label="Rechercher une app" />
      </label>
      {admin && inNode.some((i) => i.external) && (
        <div className="segmented segmented-small" role="group" aria-label="Type">
          {(
            [
              ['all', 'Tout'],
              ['apps', 'Forgeyard'],
              ['external', 'Externes'],
            ] as const
          ).map(([value, label]) => (
            <button key={value} type="button" aria-pressed={kind === value} className={kind === value ? 'on' : ''} onClick={() => setKind(value)}>
              {label}
            </button>
          ))}
        </div>
      )}
      <div className="segmented segmented-small" role="group" aria-label="État">
        {(
          [
            ['all', 'Toutes', byKind.length],
            ['up', 'En ligne', count('up')],
            ['stopped', 'Arrêtées', count('stopped')],
            ['problem', 'En erreur', count('problem')],
          ] as const
        ).map(([value, label, n]) => (
          <button key={value} type="button" aria-pressed={stateFilter === value} className={stateFilter === value ? 'on' : ''} onClick={() => setStateFilter(value)}>
            {label} <span className={`filter-count ${value === 'problem' && n > 0 ? 'text-down' : ''}`}>{n}</span>
          </button>
        ))}
      </div>
    </div>
  )

  const empty =
    apps && shown.length === 0 ? (
      <div className="empty-state">
        {all.length === 0 ? (
          <>
            <strong>Aucune app pour le moment</strong>
            <span>Déployez une image Docker, par exemple nginx:alpine sur le port 80.</span>
          </>
        ) : (
          <span>{inNode.length === 0 ? 'Aucune app sur ce node.' : 'Rien ne correspond aux filtres.'}</span>
        )}
      </div>
    ) : null

  const title = node ? node.name : admin ? 'Toutes les apps' : 'Mes apps'
  const nodeDot = (n: Node) => <Dot tone={n.state === 'online' ? 'up' : n.state === 'offline' ? 'down' : ''} />
  const createModal = creating && (
    <AppFormModal
      title="Nouvelle app"
      admin={admin}
      submitLabel="Créer et démarrer"
      onClose={() => setCreating(false)}
      onSubmit={async (input, logo) => {
        const app = await api.createApp(input)
        if (logo) await api.setAppLogo(app.id, { mode: 'custom', image: logo }).catch(() => {})
        setCreating(false)
        onChange()
        go(`apps/${app.id}`)
      }}
    />
  )

  if (layout === 'launcher') {
    return (
      <div className="section">
        <div className="launcher-bar">
          {admin && (
            <nav className="launcher-tabs" role="tablist" aria-label="Nodes">
              {choices.map((n) => (
                <button key={n.id} type="button" role="tab" aria-selected={node?.id === n.id} className={node?.id === n.id ? 'on' : ''} onClick={() => setPicked(String(n.id))}>
                  {nodeDot(n)}
                  {n.name}
                  <span className="muted">{all.filter((i) => nodeOf(i) === n.id).length}</span>
                </button>
              ))}
              <button type="button" role="tab" aria-selected={!node} className={!node ? 'on' : ''} onClick={() => setPicked('all')}>
                Tous
              </button>
            </nav>
          )}
          {!admin && <h1 className="toolbar-title">Mes apps</h1>}
          {newApp}
        </div>
        {filters}
        {error && <p className="error">{error}</p>}
        {empty}
        {shown.length > 0 && (
          <div className="launcher-grid">
            {shown.map((i) => (
              <WithMenu key={i.key} item={i} admin={admin} onChange={onChange} place="launcher">
                <LauncherIcon item={i} />
              </WithMenu>
            ))}
            <button type="button" className="launcher-icon launcher-new" onClick={() => setCreating(true)}>
              <span className="launcher-plus" aria-hidden="true">
                +
              </span>
              <span>Nouvelle app</span>
            </button>
          </div>
        )}
        {createModal}
      </div>
    )
  }

  if (layout === 'nodes') {
    return (
      <div className="section">
        <div className="toolbar">
          <h1 className="toolbar-title">Apps</h1>
          {newApp}
        </div>
        {admin && (
          <div className="node-picker">
            {choices.map((n) => {
              const list = all.filter((i) => nodeOf(i) === n.id)
              const p = problems(n.id)
              return (
                <button key={n.id} type="button" aria-pressed={node?.id === n.id} className={`node-pick ${node?.id === n.id ? 'on' : ''}`} onClick={() => setPicked(String(n.id))}>
                  <span className="node-pick-head">
                    {nodeDot(n)}
                    <strong>{n.name}</strong>
                    {n.isLocal && <span className="muted">cette machine</span>}
                  </span>
                  <span className="logo-stack">
                    {list.slice(0, 4).map((i) => (
                      <ItemLogo key={i.key} item={i} size={28} />
                    ))}
                    {list.length > 4 && <span className="logo-more">+{list.length - 4}</span>}
                  </span>
                  <span className="muted node-pick-meta">
                    {list.length} app{list.length > 1 ? 's' : ''}
                    {p > 0 ? <span className="text-down"> · {p} en erreur</span> : n.state === 'offline' ? ' · hors ligne' : ''}
                  </span>
                </button>
              )
            })}
            <button type="button" aria-pressed={!node} className={`node-pick node-pick-all ${!node ? 'on' : ''}`} onClick={() => setPicked('all')}>
              <span className="node-pick-head">
                <strong>Tous les nodes</strong>
              </span>
              <span className="muted node-pick-meta">Les {all.length} apps ensemble, pour chercher sans savoir où elles tournent.</span>
            </button>
          </div>
        )}
        <section className="panel apps-panel">
          <div className="apps-panel-head">
            <h2>{title}</h2>
            <span className="muted">{inNode.length} apps</span>
          </div>
          {filters}
          {error && <p className="error">{error}</p>}
          {empty}
          {shown.length > 0 && (
            <div className="tile-grid">
              {shown.map((i) => (
                <WithMenu key={i.key} item={i} admin={admin} onChange={onChange} place="tile">
                  <AppTile item={i} />
                </WithMenu>
              ))}
            </div>
          )}
        </section>
        {createModal}
      </div>
    )
  }

  // "sidebar", the default: nodes on the left, the picked node's apps on the right.
  return (
    <div className={admin ? 'apps-split' : 'section'}>
      {admin && (
        <nav className="node-nav" aria-label="Nodes">
          <span className="node-nav-title">Nodes</span>
          <button type="button" aria-current={!node ? 'page' : undefined} onClick={() => setPicked('all')}>
            <Dot tone="" />
            <span className="node-nav-name">Tous</span>
            {problems() > 0 && <span className="problem-count">{problems()}</span>}
            <span className="muted">{all.length}</span>
          </button>
          {choices.map((n) => (
            <button key={n.id} type="button" aria-current={node?.id === n.id ? 'page' : undefined} onClick={() => setPicked(String(n.id))}>
              {nodeDot(n)}
              <span className="node-nav-name">{n.name}</span>
              {problems(n.id) > 0 && <span className="problem-count">{problems(n.id)}</span>}
              <span className="muted">{all.filter((i) => nodeOf(i) === n.id).length}</span>
            </button>
          ))}
          {node?.metrics && (
            <div className="node-nav-meters">
              <MiniMeter label="CPU" text={`${node.metrics.cpuPercent.toFixed(0)} %`} pct={node.metrics.cpuPercent} />
              <MiniMeter
                label="RAM"
                text={`${formatBytes(node.metrics.memoryUsedBytes)} / ${formatBytes(node.memoryBytes)}`}
                pct={(node.metrics.memoryUsedBytes / (node.memoryBytes || 1)) * 100}
              />
            </div>
          )}
        </nav>
      )}
      <main className="apps-main">
        <div className="toolbar">
          <h1 className="toolbar-title">
            {title} <span className="muted">{inNode.length} apps</span>
          </h1>
          {newApp}
        </div>
        {filters}
        {error && <p className="error">{error}</p>}
        {empty}
        {shown.length > 0 && (
          <div className="card-grid">
            {shown.map((i) => (
              <WithMenu key={i.key} item={i} admin={admin} onChange={onChange} place="card">
                <AppCard item={i} admin={admin} />
              </WithMenu>
            ))}
          </div>
        )}
      </main>
      {createModal}
    </div>
  )
}

function MiniMeter({ label, text, pct }: { label: string; text: string; pct: number }) {
  const tone = pct >= 90 ? 'down' : pct >= 75 ? 'warn' : 'ok'
  return (
    <div className="mini-meter">
      <div className="meter-label">
        <span>{label}</span>
        <span className="muted">{text}</span>
      </div>
      <div className="meter-track">
        <div className={`meter-fill meter-${tone}`} style={{ width: `${Math.min(100, pct)}%` }} />
      </div>
    </div>
  )
}

function ItemLogo({ item, size }: { item: Item; size: number }) {
  return item.app ? (
    <AppLogo url={item.app.logo.url} color={item.app.logo.color} name={item.name} size={size} faded={item.group === 'stopped'} />
  ) : (
    <AppLogo url={item.container?.logoUrl} name={item.name} size={size} faded={item.group === 'stopped'} />
  )
}

function itemHref(i: Item): string {
  return i.app ? `#/apps/${i.app.id}` : `#/containers/${i.container!.nodeId}/${i.container!.id}`
}

function itemState(i: Item): string {
  return i.app ? stateText(i.app) : containerText(i.container!)
}

/** The confirmation of an app's deletion, which takes its data with it. */
function deleteQuestion(app: App): string {
  const data = app.volumes.length ? ` et ses données (${app.volumes.map((v) => v.path).join(', ')})` : ''
  return `Supprimer l’app « ${app.name} » ? Son conteneur, son enregistrement DNS${data} seront supprimés.`
}

/** An app of the list with a "⋯" menu of its actions; external containers have none. */
function WithMenu({ item, admin, onChange, place, children }: { item: Item; admin: boolean; onChange: () => void; place: 'card' | 'tile' | 'launcher'; children: ReactNode }) {
  const app = item.app
  const [open, setOpen] = useState(false)
  const [moving, setMoving] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent | KeyboardEvent) => {
      if (e instanceof KeyboardEvent ? e.key === 'Escape' : !ref.current?.contains(e.target as globalThis.Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', close)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', close)
    }
  }, [open])
  if (!app) return <>{children}</>

  const act = (action: () => Promise<unknown>) => async () => {
    setOpen(false)
    try {
      await action()
      onChange()
    } catch (err) {
      window.alert(errorMessage(err))
    }
  }
  return (
    <div className={`menu-wrap menu-${place}`} ref={ref}>
      {children}
      <button type="button" className={`item-menu-btn ${open ? 'open' : ''}`} aria-label={`Actions de ${app.name}`} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <circle cx="5" cy="12" r="2" />
          <circle cx="12" cy="12" r="2" />
          <circle cx="19" cy="12" r="2" />
        </svg>
      </button>
      {open && (
        <div className="item-menu" role="menu">
          {app.url && (
            <a role="menuitem" href={app.url} target="_blank" rel="noopener noreferrer" onClick={() => setOpen(false)}>
              Ouvrir le site
            </a>
          )}
          {app.running ? (
            <button type="button" role="menuitem" onClick={act(() => api.appAction(app.id, 'stop'))}>
              Arrêter
            </button>
          ) : (
            <button type="button" role="menuitem" disabled={app.suspended} onClick={act(() => api.appAction(app.id, 'start'))}>
              Démarrer
            </button>
          )}
          <button type="button" role="menuitem" disabled={app.suspended} onClick={act(() => api.appAction(app.id, 'redeploy'))}>
            Redéployer
          </button>
          {admin && (
            <button
              type="button"
              role="menuitem"
              disabled={!!app.movingFrom}
              onClick={() => {
                setOpen(false)
                setMoving(true)
              }}
            >
              Changer de node…
            </button>
          )}
          <button
            type="button"
            role="menuitem"
            className="danger"
            onClick={() => {
              if (window.confirm(deleteQuestion(app))) act(() => api.deleteApp(app.id))()
              else setOpen(false)
            }}
          >
            Supprimer
          </button>
        </div>
      )}
      {moving && (
        <MoveApp
          app={app}
          onClose={() => setMoving(false)}
          onMoved={() => {
            setMoving(false)
            onChange()
          }}
        />
      )}
    </div>
  )
}

/** View B: logo, name, owner, state and address. */
function AppCard({ item: i, admin }: { item: Item; admin: boolean }) {
  const address =
    i.app?.kind === 'sandbox'
      ? 'sandbox · accès SSH'
      : (i.app?.url?.replace(/^https?:\/\//, '') ?? (i.container?.ports.length ? i.container.ports.join(' · ') : ''))
  return (
    <a href={itemHref(i)} className={`app-card ${i.tone === 'down' ? 'app-card-down' : ''} ${i.external ? 'app-card-external' : ''}`}>
      <span className="app-card-head">
        <ItemLogo item={i} size={44} />
        <span className="app-card-title">
          <strong>{i.name}</strong>
          <span className="muted">{i.external ? 'externe' : admin ? i.app!.ownerName : i.app!.image}</span>
        </span>
        <span className="app-card-state">
          <Dot tone={i.tone} />
          {itemState(i)}
        </span>
      </span>
      <span className={address ? 'app-card-url' : 'muted app-card-meta'}>{address || 'pas d’adresse web'}</span>
    </a>
  )
}

/** View A: logo, name and state. */
function AppTile({ item: i }: { item: Item }) {
  return (
    <a href={itemHref(i)} className={`app-tile ${i.tone === 'down' ? 'app-card-down' : ''} ${i.external ? 'app-card-external' : ''}`}>
      <ItemLogo item={i} size={40} />
      <span className="app-tile-text">
        <strong>{i.name}</strong>
        <span className={`app-tile-state state-${i.tone}`}>
          {i.external && 'externe · '}
          {itemState(i)}
        </span>
      </span>
    </a>
  )
}

/** View C: a big logo with a state badge, like a phone's home screen. */
function LauncherIcon({ item: i }: { item: Item }) {
  return (
    <a href={itemHref(i)} className="launcher-icon" title={`${i.name} · ${itemState(i)}`}>
      <span className="launcher-logo">
        <ItemLogo item={i} size={64} />
        <span className={`launcher-badge ${i.tone ? `dot-${i.tone}` : ''}`} aria-hidden="true" />
      </span>
      <strong>{i.name}</strong>
      {i.external && <span className="muted launcher-sub">externe</span>}
    </a>
  )
}

/** The Docker Hub logo of an image, as the server serves it (see hubRepo on the server). */
function hubLogoURL(image: string): string | undefined {
  let ref = image.trim().toLowerCase().split('@')[0]
  const colon = ref.lastIndexOf(':')
  if (colon > ref.lastIndexOf('/')) ref = ref.slice(0, colon)
  let parts = ref.split('/').filter(Boolean)
  if (parts.length > 1 && (/[.:]/.test(parts[0]) || parts[0] === 'localhost')) {
    if (!['docker.io', 'index.docker.io', 'registry-1.docker.io'].includes(parts[0])) return undefined
    parts = parts.slice(1)
  }
  if (parts.length === 1) parts = ['library', parts[0]]
  return parts.length === 2 ? `/api/logos?repo=${encodeURIComponent(parts.join('/'))}` : undefined
}

function nodeChoiceLabel(c: NodeChoice): string {
  return `${c.name} · ${formatBytes(c.freeMemoryBytes)} libres · ${c.cpus} CPU · ${c.apps} app${c.apps > 1 ? 's' : ''}${c.recommended ? ' (recommandée)' : ''}`
}

type Source = 'image' | 'dockerfile' | 'sandbox'

// The systems a sandbox starts from in one click; any other image works too.
const sandboxSystems = [
  { image: 'debian:bookworm', label: 'Debian 12' },
  { image: 'ubuntu:24.04', label: 'Ubuntu 24.04' },
  { image: 'alpine:3.21', label: 'Alpine 3.21' },
]

const sourceIcons: Record<string, ReactNode> = {
  // A whale carrying containers.
  image: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" aria-hidden="true">
      <path d="M2.5 12.5h16.8c.9-1.6 2.2-2.2 3.2-2.1-.2 1.2-.9 2-1.9 2.3-1.3 4.3-5 6.8-10 6.8-4.6 0-7.4-2.8-8.1-7Z" />
      <path d="M5 9.5h3v3H5zM8 9.5h3v3H8zM11 9.5h3v3h-3zM8 6.5h3v3H8zM11 6.5h3v3h-3z" strokeWidth="1.4" />
    </svg>
  ),
  dockerfile: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8Z" />
      <path d="M14 3v5h5M9 13h6M9 17h4" />
    </svg>
  ),
  sandbox: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="m7 9 3 3-3 3M13 15h4" />
    </svg>
  ),
  github: (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M12 .3a12 12 0 0 0-3.8 23.4c.6.1.8-.3.8-.6v-2c-3.3.7-4-1.6-4-1.6-.6-1.4-1.4-1.8-1.4-1.8-1-.7.1-.7.1-.7 1.2.1 1.8 1.2 1.8 1.2 1.1 1.8 2.8 1.3 3.5 1 .1-.8.4-1.3.8-1.6-2.7-.3-5.5-1.3-5.5-5.9 0-1.3.5-2.4 1.2-3.2-.1-.3-.5-1.5.1-3.2 0 0 1-.3 3.3 1.2a11.5 11.5 0 0 1 6 0C17.3 4.7 18.3 5 18.3 5c.6 1.7.2 2.9.1 3.2.8.8 1.2 1.9 1.2 3.2 0 4.6-2.8 5.6-5.5 5.9.4.4.8 1.1.8 2.2v3.3c0 .3.2.7.8.6A12 12 0 0 0 12 .3" />
    </svg>
  ),
}

const sources: { key: Source | 'sandbox' | 'github'; label: string; soon?: boolean }[] = [
  { key: 'image', label: 'Image' },
  { key: 'dockerfile', label: 'Dockerfile' },
  { key: 'sandbox', label: 'Sandbox' },
  { key: 'github', label: 'GitHub', soon: true },
]

const dockerfileExample = `FROM nginx:alpine
RUN echo 'Bonjour !' > /usr/share/nginx/html/index.html`

/** The image a Dockerfile starts from, its first FROM: its logo is the app's. */
function dockerfileBase(dockerfile: string): string {
  for (const line of dockerfile.split('\n')) {
    let f = line.trim().split(/\s+/)
    if (f.length < 2 || f[0].toUpperCase() !== 'FROM') continue
    f = f.slice(1)
    while (f.length > 1 && f[0].startsWith('--')) f = f.slice(1)
    return f[0]
  }
  return ''
}

/** The ports a Dockerfile exposes, from its EXPOSE lines (TCP only). */
function dockerfileExposed(dockerfile: string): number[] {
  const ports: number[] = []
  for (const line of dockerfile.split('\n')) {
    const f = line.trim().split(/\s+/)
    if (f[0]?.toUpperCase() !== 'EXPOSE') continue
    for (const p of f.slice(1)) {
      const [num, proto] = p.split('/')
      const n = Number(num)
      if (Number.isInteger(n) && n > 0 && n < 65536 && (!proto || proto === 'tcp') && !ports.includes(n)) ports.push(n)
    }
  }
  return ports
}

function AppFormModal({
  title,
  submitLabel,
  initial,
  admin,
  onSubmit,
  onClose,
}: {
  title: string
  submitLabel: string
  initial?: App
  // Admins pick the node of a new app; users' apps go to the recommended one.
  admin?: boolean
  // logo: a framed picture chosen at creation, as a data URL.
  onSubmit: (input: AppInput, logo?: string) => Promise<void>
  onClose: () => void
}) {
  const [source, setSource] = useState<Source>(initial?.kind === 'sandbox' ? 'sandbox' : initial?.dockerfile ? 'dockerfile' : 'image')
  const [name, setName] = useState(initial?.name ?? '')
  const [image, setImage] = useState(initial?.dockerfile ? '' : (initial?.image ?? ''))
  const [dockerfile, setDockerfile] = useState(initial?.dockerfile ?? '')
  const [logo, setLogo] = useState('')
  const [choices, setChoices] = useState<NodeChoice[]>([])
  const [nodeId, setNodeId] = useState(0)
  const [domain, setDomain] = useState('')
  useEffect(() => {
    if (initial) return
    api.instance().then((i) => setDomain(i.appsDomain ?? ''), () => {})
    if (!admin) return
    api.nodeChoices().then((c) => {
      setChoices(c)
      setNodeId(c.find((x) => x.recommended)?.id ?? 0)
    }, () => {})
  }, [admin, initial])
  // The logo preview follows the image once typing pauses, not at every key (each name is looked up).
  const logoImage = source === 'dockerfile' ? dockerfileBase(dockerfile) : image
  const sandbox = source === 'sandbox'
  const [settledImage, setSettledImage] = useState(logoImage)
  useEffect(() => {
    const t = setTimeout(() => setSettledImage(logoImage), 600)
    return () => clearTimeout(t)
  }, [logoImage])
  const [cropping, setCropping] = useState<File | null>(null)
  const logoFile = useRef<HTMLInputElement>(null)
  const [port, setPort] = useState(String(initial?.port ?? 80))
  // The port fills in from what the image (or the Dockerfile) declares, until it is typed by hand.
  const [portTouched, setPortTouched] = useState(!!initial)
  const [declared, setDeclared] = useState<{ image: string; ports: number[]; volumes: string[]; error?: string } | null>(null)
  useEffect(() => {
    if (!settledImage.trim() || sandbox) return
    let live = true
    api.imagePorts(settledImage).then(
      (r) => live && setDeclared({ image: settledImage, ports: r.ports, volumes: r.volumes ?? [], error: r.error }),
      (err) => live && setDeclared({ image: settledImage, ports: [], volumes: [], error: errorMessage(err) }),
    )
    return () => {
      live = false
    }
  }, [settledImage, sandbox])
  const exposed = source === 'dockerfile' ? dockerfileExposed(dockerfile) : []
  const fromImage = declared?.image === settledImage ? declared.ports : null
  const suggested = exposed.length ? exposed : (fromImage ?? [])
  const suggestedKey = suggested.join(',')
  useEffect(() => {
    if (!portTouched && suggestedKey) setPort(suggestedKey.split(',')[0])
  }, [suggestedKey, portTouched])
  const [memory, setMemory] = useState(String(initial?.memoryMb ?? 512))
  const [env, setEnv] = useState<{ key: string; value: string }[]>(Object.entries(initial?.env ?? {}).map(([key, value]) => ({ key, value })))
  // Volumes start with the paths the image keeps (its VOLUMEs), until they are edited by hand.
  const [volumes, setVolumes] = useState<string[]>((initial?.volumes ?? []).filter((v) => !v.builtin).map((v) => v.path))
  const [volumesTouched, setVolumesTouched] = useState(!!initial)
  const [volumesOpen, setVolumesOpen] = useState(volumes.length > 0)
  const declaredVolumes = declared?.image === settledImage ? declared.volumes : []
  const declaredKey = declaredVolumes.join('\n')
  useEffect(() => {
    if (volumesTouched || !declaredKey) return
    setVolumes(declaredKey.split('\n'))
    setVolumesOpen(true)
  }, [declaredKey, volumesTouched])
  const editVolumes = (v: string[]) => {
    setVolumes(v)
    setVolumesTouched(true)
  }
  const keptCount = volumes.filter((v) => v.trim()).length
  const [envOpen, setEnvOpen] = useState(env.length > 0)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const vars: Record<string, string> = {}
      for (const { key, value } of env) if (key.trim()) vars[key.trim()] = value
      await onSubmit(
        {
          name: initial ? undefined : name,
          kind: initial ? undefined : sandbox ? 'sandbox' : 'web',
          image: source === 'dockerfile' ? '' : image,
          dockerfile: source === 'dockerfile' ? dockerfile : '',
          port: Number(port),
          memoryMb: Number(memory),
          env: vars,
          volumes: volumes.map((v) => v.trim()).filter(Boolean),
          nodeId: nodeId || undefined,
        },
        logo || undefined,
      )
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  const shownName = initial?.name ?? (name || 'mon-app')
  const address = sandbox
    ? (initial?.ssh ?? `ssh ${shownName}@… -p 2222`)
    : (initial?.url?.replace(/^https?:\/\//, '') ?? (domain ? `${shownName}.${domain}` : ''))
  const node = initial ? initial.nodeName : choices.find((c) => c.id === nodeId)
  const setCount = env.filter((v) => v.key.trim()).length

  const chosen = typeof node === 'object' ? node : undefined
  // The memory limit is a ceiling, not a reservation: an app uses what it needs, so a limit above the free
  // memory only warns. Without a measure yet, the row is left out rather than shown as full.
  const free = chosen?.freeMemoryBytes || undefined
  const overFree = free !== undefined && Number(memory) * 1024 * 1024 > free
  const aside = (
    <>
      <span className="aside-label">Aperçu</span>
      <div className="app-preview">
        <span className="app-preview-logo">
          {initial ? (
            <AppLogo url={initial.logo.url} color={initial.logo.color} name={initial.name} size={72} />
          ) : logo ? (
            <img className="app-logo" src={logo} alt="" style={{ width: 72, height: 72 }} />
          ) : (
            <AppLogo url={hubLogoURL(settledImage)} name={shownName} size={72} />
          )}
        </span>
        <strong className="app-preview-name">{shownName}</strong>
        {address ? <span className="app-preview-url">{address}</span> : <span className="muted app-preview-meta">pas encore d’adresse web</span>}
        {!initial && (
          <span className="app-preview-meta muted">
            {logo ? 'Ton logo' : 'Logo de l’image'} ·{' '}
            <button type="button" className="link-button" onClick={() => logoFile.current?.click()}>
              changer
            </button>
            {logo && (
              <>
                {' · '}
                <button type="button" className="link-button" onClick={() => setLogo('')}>
                  automatique
                </button>
              </>
            )}
          </span>
        )}
      </div>
      {choices.length > 1 && (
        <label className="field">
          <span>Node</span>
          <select value={nodeId} onChange={(e) => setNodeId(Number(e.target.value))}>
            {choices.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
                {c.recommended ? ' (recommandé)' : ''}
              </option>
            ))}
          </select>
        </label>
      )}
      <dl className="aside-facts">
        {choices.length <= 1 && (
          <>
            <dt>Node</dt>
            <dd>{typeof node === 'string' ? node : chosen ? chosen.name : 'le plus puissant (auto)'}</dd>
          </>
        )}
        {chosen && (
          <>
            <dt>Machine</dt>
            <dd>
              {chosen.cpus} CPU · {chosen.apps} app{chosen.apps > 1 ? 's' : ''}
            </dd>
            {free !== undefined && (
              <>
                <dt>Mémoire libre</dt>
                <dd className={overFree ? 'state-warn' : ''}>{formatBytes(free)}</dd>
              </>
            )}
          </>
        )}
        <dt>Données</dt>
        <dd>{sandbox ? '/root gardé' : keptCount ? `${keptCount} volume${keptCount > 1 ? 's' : ''}` : 'non gardées'}</dd>
        <dt>Source</dt>
        <dd>{sandbox ? 'Linux, accès SSH' : source === 'dockerfile' ? 'construite' : 'téléchargée'}</dd>
      </dl>
      {overFree && (
        <p className="aside-note aside-warn">
          La limite ({memory} Mo) dépasse la mémoire libre du node : l’app démarre quand même, elle n’en prend que ce qu’elle utilise. Si elle
          monte jusque-là, elle risque d’être arrêtée faute de mémoire.
        </p>
      )}
      {initial && <p className="muted aside-note">Enregistrer redéploie l’app sans coupure.</p>}
      <span className="aside-spacer" />
      {error && <p className="error">{error}</p>}
      <button type="submit" className="btn btn-primary btn-block btn-big" disabled={busy}>
        {busy ? 'Envoi…' : submitLabel}
      </button>
      <button type="button" className="link-button aside-cancel" onClick={onClose} disabled={busy}>
        Annuler
      </button>
    </>
  )

  return (
    <Modal title={title} onClose={onClose} onSubmit={submit} aside={aside}>
      <div className="source-pick" role="radiogroup" aria-label="D’où vient l’app">
        {sources.map((s) => (
          <button
            key={s.key}
            type="button"
            role="radio"
            aria-checked={source === s.key}
            className={`source-chip ${source === s.key ? 'on' : ''}`}
            // A sandbox stays one, an app stays one: the kind is set at creation.
            disabled={s.soon || busy || (!!initial && (s.key === 'sandbox') !== (initial.kind === 'sandbox'))}
            onClick={() => {
              setSource(s.key as Source)
              if (s.key === 'sandbox' && !image.trim()) setImage(sandboxSystems[0].image)
            }}
          >
            {sourceIcons[s.key]}
            {s.label}
            {s.soon && <span className="soon">bientôt</span>}
          </button>
        ))}
      </div>
      {sandbox ? (
        <div className="field">
          <span>Système</span>
          <div className="os-pick">
            {sandboxSystems.map((o) => (
              <button key={o.image} type="button" className={`os-chip ${image === o.image ? 'on' : ''}`} onClick={() => setImage(o.image)}>
                <AppLogo url={hubLogoURL(o.image)} name={o.label} size={22} />
                {o.label}
              </button>
            ))}
          </div>
          <input className="mono" aria-label="Image" value={image} onChange={(e) => setImage(e.target.value)} placeholder="debian:bookworm" required />
          <small>
            Une machine Linux qui tourne en continu, sans adresse web : on s’y connecte en SSH avec une clé de Mon profil › Clés SSH. Son /root est
            gardé entre les redéploiements.
          </small>
        </div>
      ) : source === 'image' ? (
        <label className="field">
          <span>Image</span>
          <input className="mono" value={image} onChange={(e) => setImage(e.target.value)} placeholder="nginx:alpine" required autoFocus={!!initial} />
          <small>Une image publiée : Docker Hub, ghcr.io…</small>
        </label>
      ) : (
        <label className="field">
          <span>Dockerfile</span>
          <textarea className="mono" rows={8} value={dockerfile} onChange={(e) => setDockerfile(e.target.value)} placeholder={dockerfileExample} spellCheck={false} required />
          <small>Construit sur le node, sans autres fichiers : COPY ne trouve rien, récupère ton code avec RUN git clone ou ADD d’une URL.</small>
        </label>
      )}
      <div className="form-row form-row-app">
        {!initial && (
          <label className="field">
            <span>Nom</span>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="mon-app" pattern="[a-z0-9]([a-z0-9\-]{0,30}[a-z0-9])?" title="a-z, 0-9 et -, sans tiret au début ni à la fin" required autoFocus />
          </label>
        )}
        {!sandbox && (
          <label className="field">
            <span>Port</span>
            <input
              type="number"
              min={1}
              max={65535}
              value={port}
              onChange={(e) => {
                setPort(e.target.value)
                setPortTouched(true)
              }}
              required
              title="Le port sur lequel l’app écoute dans son conteneur"
            />
            {suggested.length > 0 && suggested.includes(Number(port)) ? (
              <small>{exposed.length ? 'Du Dockerfile ✓' : 'Lu dans l’image ✓'}</small>
            ) : suggested.length > 0 ? (
              <small>
                {exposed.length ? 'Le Dockerfile indique' : 'L’image indique'}{' '}
                {suggested.map((p) => (
                  <button key={p} type="button" className="link-button" onClick={() => setPort(String(p))}>
                    {p}
                  </button>
                ))}
              </small>
            ) : (
              fromImage && <small>{declared?.error ? `Port introuvable : ${declared.error}.` : 'L’image n’indique pas de port : voir sa doc.'}</small>
            )}
          </label>
        )}
        <label className="field">
          <span>Mémoire (Mo)</span>
          <input type="number" min={64} max={16384} step={64} value={memory} onChange={(e) => setMemory(e.target.value)} required />
        </label>
      </div>
      <details className="field" open={envOpen} onToggle={(e) => setEnvOpen(e.currentTarget.open)}>
        <summary>
          Variables d’environnement <span className="muted">{setCount ? `· ${setCount}` : '· aucune'}</span>
        </summary>
        <div className="env-list">
          {env.map((v, i) => (
            <div className="env-row" key={i}>
              <input aria-label="Nom de la variable" value={v.key} onChange={(e) => setEnv(env.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))} placeholder="NOM" />
              <input aria-label="Valeur" value={v.value} onChange={(e) => setEnv(env.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} placeholder="valeur" />
              <button type="button" className="btn btn-ghost" onClick={() => setEnv(env.filter((_, j) => j !== i))} aria-label="Retirer la variable">
                ✕
              </button>
            </div>
          ))}
          <div>
            <button type="button" className="btn btn-dashed btn-small" onClick={() => setEnv([...env, { key: '', value: '' }])}>
              + Ajouter une variable
            </button>
          </div>
          <small>Chiffrées avant d’être enregistrées.</small>
        </div>
      </details>
      <details className="field" open={volumesOpen} onToggle={(e) => setVolumesOpen(e.currentTarget.open)}>
        <summary>
          Volumes <span className="muted">{keptCount ? `· ${keptCount}` : '· aucun'}</span>
        </summary>
        <div className="env-list">
          {volumes.map((v, i) => (
            <div className="volume-row" key={i}>
              <input
                className="mono"
                aria-label="Dossier gardé"
                value={v}
                onChange={(e) => editVolumes(volumes.map((x, j) => (j === i ? e.target.value : x)))}
                placeholder="/var/lib/postgresql/data"
              />
              <button type="button" className="btn btn-ghost" onClick={() => editVolumes(volumes.filter((_, j) => j !== i))} aria-label="Retirer le volume">
                ✕
              </button>
            </div>
          ))}
          {declaredVolumes.filter((d) => !volumes.includes(d)).length > 0 && (
            <span className="chips-row">
              {declaredVolumes
                .filter((d) => !volumes.includes(d))
                .map((d) => (
                  <button key={d} type="button" className="chip mono" onClick={() => editVolumes([...volumes, d])}>
                    + {d}
                  </button>
                ))}
            </span>
          )}
          <div>
            <button type="button" className="btn btn-dashed btn-small" onClick={() => editVolumes([...volumes, ''])}>
              + Ajouter un volume
            </button>
          </div>
          <small>
            Les dossiers du conteneur gardés quand l’app est redéployée (une base de données, des fichiers envoyés…). Le reste repart de zéro.
            {declaredVolumes.length > 0 && !volumesTouched && ' Proposés par l’image.'}
            {sandbox && ' Le /root d’une sandbox est déjà gardé.'}
          </small>
        </div>
      </details>
      <input
        ref={logoFile}
        type="file"
        accept="image/png,image/jpeg,image/webp,image/gif"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0]
          e.target.value = ''
          if (f) setCropping(f)
        }}
      />
      {cropping && (
        <ImageCropper
          file={cropping}
          title="Cadrer le logo"
          outWidth={256}
          outHeight={256}
          png
          onCancel={() => setCropping(null)}
          onSave={async (dataUrl) => {
            setLogo(dataUrl)
            setCropping(null)
          }}
        />
      )}
    </Modal>
  )
}

type DetailTab = 'observability' | 'logs' | 'terminal' | 'network' | 'events'

function AppDetail({
  app,
  admin,
  nodes,
  onChange,
  go,
  openTab,
}: {
  app: App
  admin: boolean
  nodes: Node[]
  onChange: () => void
  go: (path: string) => void
  openTab?: DetailTab // #/apps/3/network opens the Réseau tab
}) {
  const [full, setFull] = useState<App | null>(null)
  const [editing, setEditing] = useState(false)
  const [tab, setTab] = useState<DetailTab>(openTab ?? 'observability')
  const [choosingLogo, setChoosingLogo] = useState(false)
  const [moving, setMoving] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.app(app.id).then(setFull).catch((err) => setError(errorMessage(err)))
  }, [app.id, app.updatedAt])

  async function run(action: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await action()
      onChange()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const tone = appTone(app)

  return (
    <div className="detail">
      <aside className="panel detail-side">
        <a href="#/apps" className="back-link">
          ← Apps
        </a>
        <div className="detail-title">
          <button type="button" className="logo-button" onClick={() => setChoosingLogo(true)} aria-label="Changer le logo" title="Changer le logo">
            <AppLogo url={app.logo.url} color={app.logo.color} name={app.name} size={52} />
            <span className="logo-edit" aria-hidden="true">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
              </svg>
            </span>
          </button>
          <div>
            <h1>{app.name}</h1>
            <span className={`state-line state-${tone}`}>
              <Dot tone={tone} />
              {stateText(app)}
              {app.state === 'running' && app.startedAt ? ` depuis ${upFor(app.startedAt)}` : ''}
            </span>
          </div>
        </div>

        {app.kind === 'sandbox' ? (
          <div className="ssh-box">
            <span className="muted">Se connecter en SSH</span>
            {app.ssh ? <CopyField value={app.ssh} /> : <span>Passerelle SSH désactivée : utilisez l’onglet Terminal.</span>}
            <small className="muted">
              Avec une clé de <a href="#/profile/ssh">Mon profil › Clés SSH</a>. Ou l’onglet Terminal, ici même.
            </small>
          </div>
        ) : app.url ? (
          <a className="btn btn-primary btn-block" href={app.url} target="_blank" rel="noopener noreferrer">
            Ouvrir {app.url.replace(/^https?:\/\//, '')}
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M7 17 17 7M8 7h9v9" />
            </svg>
          </a>
        ) : (
          <p className="hint">
            {app.hostPort
              ? `Publiée sur le port ${app.hostPort} du node ${app.nodeName}. Renseignez l’IP du node (Nodes › Réseau) ou un domaine (Réglages) pour avoir un lien.`
              : 'Pas encore d’adresse : configurez un domaine dans Réglages › Domaine des apps.'}
          </p>
        )}

        <div className="node-line">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect x="3" y="4" width="18" height="7" rx="2" />
            <rect x="3" y="13" width="18" height="7" rx="2" />
            <path d="M7 7.5h.01M7 16.5h.01" />
          </svg>
          <span className="node-line-text">
            <small className="muted">Tourne sur</small>
            <strong>{app.nodeName}</strong>
          </span>
          {admin && (
            <button type="button" className="btn btn-small" disabled={busy || !!app.movingFrom} onClick={() => setMoving(true)} title="Changer de node">
              Changer
            </button>
          )}
        </div>

        {app.movingFrom ? (
          <div className="banner banner-warn">
            <span className="dot dot-warn" />
            <span>
              Déplacement depuis {nodes.find((n) => n.id === app.movingFrom)?.name ?? 'l’ancien node'} : il continue de la servir jusqu’à ce
              qu’elle soit en ligne sur {app.nodeName}.
            </span>
          </div>
        ) : null}
        {/* The app listens elsewhere than its setting says: visitors get Bad Gateway until the port is fixed. */}
        {app.kind !== 'sandbox' && app.state === 'running' && app.listeningPorts?.length && !app.listeningPorts.includes(app.port) ? (
          <div className="banner banner-warn">
            <span className="dot dot-warn" />
            <span>
              L’app écoute sur le port {app.listeningPorts.join(', ')}, pas sur le {app.port} de sa configuration : ses visiteurs tombent
              sur « Bad Gateway ».{' '}
              {full &&
                app.listeningPorts.slice(0, 3).map((p) => (
                  <button
                    key={p}
                    type="button"
                    className="link-button"
                    disabled={busy}
                    onClick={() =>
                      run(async () =>
                        setFull(
                          await api.updateApp(app.id, {
                            image: full.dockerfile ? '' : full.image,
                            dockerfile: full.dockerfile ?? '',
                            port: p,
                            memoryMb: full.memoryMb,
                            env: full.env ?? {},
                          }),
                        ),
                      )
                    }
                  >
                    Utiliser {p}
                  </button>
                ))}
            </span>
          </div>
        ) : null}
        {app.crashSuspended && !app.running && (
          <div className="banner banner-down">
            <span className="dot dot-down" />
            <span>
              Forgeyard l’a arrêtée après plusieurs crashs en quelques minutes. Regardez les logs et les événements, corrigez, puis
              cliquez sur Démarrer.
            </span>
          </div>
        )}
        {app.suspended && (
          <div className="banner banner-warn">
            <span className="dot dot-warn" />
            Les apps de {app.ownerName} sont suspendues par un admin.
          </div>
        )}
        {app.error && <p className="error">{app.error}</p>}

        <dl className="kv">
          <dt>Image</dt>
          <dd className="mono">{app.image}</dd>
          {app.kind !== 'sandbox' && (
            <>
              <dt>Port</dt>
              <dd>{app.port}</dd>
            </>
          )}
          {app.kind !== 'sandbox' && app.ssh && (
            <>
              <dt>SSH</dt>
              <dd className="mono" title="Avec une clé de Mon profil › Clés SSH">
                {app.ssh}
              </dd>
            </>
          )}
          <dt>Mémoire</dt>
          <dd>{app.memoryMb} Mo max.</dd>
          <dt>Données</dt>
          <dd>
            {app.volumes.length ? (
              <span className="volume-list">
                {app.volumes.map((v) => (
                  <span key={v.path}>
                    <span className="mono">{v.path}</span>
                    {v.sizeBytes >= 0 && <span className="muted"> · {formatBytes(v.sizeBytes)}</span>}
                  </span>
                ))}
              </span>
            ) : (
              <span className="muted" title="Ajoutez un volume dans Configuration pour garder un dossier">
                non gardées au redéploiement
              </span>
            )}
          </dd>
          <dt>Propriétaire</dt>
          <dd>
            <a href={`#/u/${app.ownerId}`}>{app.ownerName}</a>
          </dd>
        </dl>
        <div className="setting-row small-setting">
          <span className="header-title">
            <strong>Visible sur le profil</strong>
            <span className="muted"> de {app.ownerName}</span>
          </span>
          <Switch checked={app.public} disabled={busy} onChange={(v) => run(() => api.setAppPublic(app.id, v))} label="Visible sur le profil du propriétaire" />
        </div>

        {error && <p className="error">{error}</p>}
        {/* Running: stop or redeploy it. Stopped: starting it deploys it afresh, so it is the only action. */}
        <div className="detail-actions">
          {app.running ? (
            <>
              <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.appAction(app.id, 'stop'))}>
                Arrêter
              </button>
              <button type="button" className="btn" disabled={busy || app.suspended} onClick={() => run(() => api.appAction(app.id, 'redeploy'))}>
                Redéployer
              </button>
            </>
          ) : (
            <button
              type="button"
              className="btn btn-primary detail-actions-wide"
              disabled={busy || app.suspended}
              title={app.suspended ? 'Les apps de ce compte sont suspendues' : undefined}
              onClick={() => run(() => api.appAction(app.id, 'start'))}
            >
              Démarrer
            </button>
          )}
          <button type="button" className="btn detail-actions-wide" disabled={busy || !full} onClick={() => setEditing(true)}>
            Configuration
          </button>
        </div>
        <button
          type="button"
          className="link-button danger-link"
          disabled={busy}
          onClick={() => {
            if (window.confirm(deleteQuestion(app))) {
              run(async () => {
                await api.deleteApp(app.id)
                go('apps')
              })
            }
          }}
        >
          Supprimer l’app…
        </button>
      </aside>

      <main className="detail-main">
        <DetailTabs tab={tab} onTab={setTab} label="Vues de l’app" withTerminal withNetwork />
        {tab === 'observability' && (
          <Observability
            m={{
              key: `app-${app.id}`,
              load: () => api.appUsage(app.id),
              running: app.state === 'running',
              cpuPercent: app.cpuPercent,
              memoryUsedBytes: app.memoryUsedBytes,
              memoryLimitBytes: app.memoryMb * 1024 * 1024,
              lifecycle: true,
              restartCount: app.restartCount,
              startedAt: app.startedAt,
            }}
          />
        )}
        {tab === 'logs' && <Logs url={`/api/apps/${app.id}/logs`} />}
        {tab === 'terminal' &&
          (app.state === 'running' ? (
            <Suspense fallback={<div className="empty-state">Chargement du terminal…</div>}>
              <Terminal path={`/api/apps/${app.id}/terminal`} />
            </Suspense>
          ) : (
            <div className="empty-state">L’app doit être en ligne pour ouvrir un terminal.</div>
          ))}
        {tab === 'network' && <AppNetworkTab app={app} onChange={onChange} />}
        {tab === 'events' && <Events eventsKey={`app-${app.id}`} load={() => api.appEvents(app.id)} />}
      </main>

      {moving && (
        <MoveApp
          app={app}
          onClose={() => setMoving(false)}
          onMoved={() => {
            setMoving(false)
            onChange()
          }}
        />
      )}
      {choosingLogo && (
        <LogoChooser
          app={app}
          onClose={() => setChoosingLogo(false)}
          onSaved={() => {
            setChoosingLogo(false)
            onChange()
          }}
        />
      )}
      {editing && full && (
        <AppFormModal
          title={`Configuration de ${app.name}`}
          submitLabel="Enregistrer et redéployer"
          initial={full}
          onClose={() => setEditing(false)}
          onSubmit={async (input) => {
            setFull(await api.updateApp(app.id, input))
            setEditing(false)
            onChange()
          }}
        />
      )}
    </div>
  )
}

/** What the observability view shows: the current measures, and how to load the last hour. */
type Measures = {
  key: string
  load: () => Promise<AppUsage>
  running: boolean
  cpuPercent?: number
  memoryUsedBytes?: number
  memoryLimitBytes?: number
  // Restarts and start time are known for Forgeyard's apps only.
  lifecycle?: boolean
  restartCount?: number
  startedAt?: number
}

const timeFormat = (t: number) => new Date(t * 1000).toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit', second: '2-digit' })

function Observability({ m }: { m: Measures }) {
  const [usage, setUsage] = useState<AppUsage | null>(null)

  useEffect(() => {
    const load = () => m.load().then(setUsage, () => {})
    load()
    const id = setInterval(load, USAGE_POLL_MS)
    return () => clearInterval(id)
  }, [m.key])

  // No memory means no measure: the agent could not read the container's use.
  const measured = m.running && !!m.memoryUsedBytes
  const limit = usage?.memoryLimitBytes || m.memoryLimitBytes || 0
  const samples = usage?.samples ?? []
  const times = samples.map((s) => s.t)

  return (
    <>
      <div className="tiles">
        <div className="tile">
          <div className="k">CPU</div>
          <div className="v">{measured ? `${(m.cpuPercent ?? 0).toFixed(1)} %` : '–'}</div>
        </div>
        <div className="tile">
          <div className="k">Mémoire</div>
          <div className="v">
            {measured ? formatBytes(m.memoryUsedBytes!) : '–'} {limit > 0 && <small>/ {formatBytes(limit)}</small>}
          </div>
        </div>
        {m.lifecycle && (
          <>
            <div className="tile">
              <div className="k">Redémarrages</div>
              <div className={`v ${m.restartCount ? 'v-down' : ''}`}>{m.restartCount ?? 0}</div>
            </div>
            <div className="tile">
              <div className="k">Démarrée</div>
              <div className="v v-small">{m.running && m.startedAt ? since(m.startedAt) : '–'}</div>
            </div>
          </>
        )}
      </div>
      {m.running && !measured && (
        <p className="hint">
          Pas de mesure pour l’instant. Si ça dure, Docker ne fournit pas l’utilisation de ce conteneur sur ce node : vérifiez avec{' '}
          <code>docker stats --no-stream</code> sur la machine.
        </p>
      )}
      <section className="panel chart-panel">
        <div className="chart-head">
          <h2>CPU</h2>
          <span className="muted">dernière heure · en % d’un cœur</span>
        </div>
        <AreaChart values={samples.map((s) => s.cpu)} times={times} format={(v) => `${v.toFixed(2)} %`} max={5} label="CPU sur la dernière heure" timeFormat={timeFormat} />
      </section>
      <section className="panel chart-panel">
        <div className="chart-head">
          <h2>Mémoire</h2>
          <span className="muted">dernière heure{limit > 0 ? ` · limite ${formatBytes(limit)}` : ''}</span>
        </div>
        <AreaChart
          values={samples.map((s) => s.mem)}
          times={times}
          format={(v) => formatBytes(v) + (limit > 0 ? ` (${Math.round((v / limit) * 100)} % de la limite)` : '')}
          max={limit || undefined}
          color="var(--warn)"
          label="Mémoire sur la dernière heure"
          timeFormat={timeFormat}
        />
      </section>
    </>
  )
}

function Events({ eventsKey, load }: { eventsKey: string; load: () => Promise<AppEvent[]> }) {
  const [events, setEvents] = useState<AppEvent[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const fetch = () =>
      load().then(
        (e) => {
          setEvents(e)
          setError('')
        },
        (err) => setError(errorMessage(err)),
      )
    fetch()
    const id = setInterval(fetch, POLL_MS * 2)
    return () => clearInterval(id)
  }, [eventsKey])

  if (error) return <p className="error">{error}</p>
  if (!events) return null
  if (events.length === 0) return <div className="empty-state">Aucun événement pour le moment : démarrages, arrêts, crashs et changements s’afficheront ici.</div>
  const tones = { info: '', success: 'up', warning: 'warn', error: 'down' }
  return (
    <ol className="panel events">
      {events.map((e, i) => (
        <li key={i}>
          <time dateTime={new Date(e.at * 1000).toISOString()}>
            {new Date(e.at * 1000).toLocaleString('fr-FR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })}
          </time>
          <Dot tone={tones[e.kind]} />
          <span>{e.message}</span>
        </li>
      ))}
    </ol>
  )
}

function DetailTabs({
  tab,
  onTab,
  label,
  withTerminal,
  withNetwork,
}: {
  tab: DetailTab
  onTab: (t: DetailTab) => void
  label: string
  withTerminal?: boolean
  withNetwork?: boolean
}) {
  return (
    <div className="segmented" role="tablist" aria-label={label}>
      {(
        [
          ['observability', 'Observabilité'],
          ['logs', 'Logs'],
          ...(withTerminal ? ([['terminal', 'Terminal']] as const) : []),
          ...(withNetwork ? ([['network', 'Réseau']] as const) : []),
          ['events', 'Événements'],
        ] as const
      ).map(([value, text]) => (
        <button key={value} type="button" role="tab" aria-selected={tab === value} className={tab === value ? 'on' : ''} onClick={() => onTab(value)}>
          {text}
        </button>
      ))}
    </div>
  )
}

function ContainerDetail({ container: c, superadmin, onChange, go }: { container: Container; superadmin: boolean; onChange: () => void; go: (path: string) => void }) {
  const [tab, setTab] = useState<DetailTab>('observability')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const tone = containerTone(c)

  async function act(action: 'start' | 'stop' | 'restart') {
    setBusy(true)
    setError('')
    try {
      await api.containerAction(c, action)
      // The node reports the new state within a few seconds.
      setTimeout(onChange, 1500)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="detail">
      <aside className="panel detail-side">
        <a href="#/apps" className="back-link" onClick={() => go('apps')}>
          ← Apps
        </a>
        <div className="detail-title">
          <AppLogo url={c.logoUrl} name={c.name} size={52} />
          <div>
            <h1>
              {c.name} <span className="badge">externe</span>
            </h1>
            <span className={`state-line state-${tone}`}>
              <Dot tone={tone} />
              {c.status}
            </span>
          </div>
        </div>
        <p className="hint">Conteneur lancé hors de Forgeyard (à la main, par docker compose…). Il appartient au superadmin.</p>
        <dl className="kv">
          <dt>Image</dt>
          <dd className="mono">{c.image}</dd>
          <dt>Node</dt>
          <dd>{c.nodeName}</dd>
          <dt>Ports</dt>
          <dd>{c.ports.length ? c.ports.join(', ') : 'aucun publié'}</dd>
          {c.composeProject && (
            <>
              <dt>Compose</dt>
              <dd>{c.composeProject}</dd>
            </>
          )}
          <dt>Propriétaire</dt>
          <dd>{c.ownerName}</dd>
        </dl>
        {error && <p className="error">{error}</p>}
        {superadmin ? (
          <div className="detail-actions">
            {c.state === 'running' ? (
              <button type="button" className="btn" disabled={busy} onClick={() => act('stop')}>
                Arrêter
              </button>
            ) : (
              <button type="button" className="btn" disabled={busy} onClick={() => act('start')}>
                Démarrer
              </button>
            )}
            <button type="button" className="btn" disabled={busy} onClick={() => act('restart')}>
              Redémarrer
            </button>
          </div>
        ) : (
          <p className="muted">Seul le superadmin peut agir sur ce conteneur.</p>
        )}
      </aside>
      <main className="detail-main">
        <DetailTabs tab={tab} onTab={setTab} label="Vues du conteneur" withTerminal={superadmin} />
        {tab === 'observability' && (
          <Observability
            m={{
              key: `c-${c.nodeId}-${c.name}`,
              load: () => api.containerUsage(c),
              running: c.state === 'running',
              cpuPercent: c.cpuPercent,
              memoryUsedBytes: c.memoryUsedBytes,
            }}
          />
        )}
        {tab === 'logs' &&
          (superadmin ? (
            <Logs url={`/api/admin/nodes/${c.nodeId}/containers/${c.id}/logs`} />
          ) : (
            <div className="empty-state">Les logs des conteneurs externes sont réservés au superadmin.</div>
          ))}
        {tab === 'terminal' &&
          (c.state === 'running' ? (
            <Suspense fallback={<div className="empty-state">Chargement du terminal…</div>}>
              <Terminal path={`/api/admin/nodes/${c.nodeId}/containers/${c.id}/terminal`} />
            </Suspense>
          ) : (
            <div className="empty-state">Le conteneur doit être en marche pour ouvrir un terminal.</div>
          ))}
        {tab === 'events' && <Events eventsKey={`c-${c.nodeId}-${c.name}`} load={() => api.containerEvents(c)} />}
      </main>
    </div>
  )
}

type LogLine = { text: string; stderr?: boolean; time?: string }

// Docker puts the time of each line in front of it (timestamps=1): 2026-10-10T14:03:22.123456789Z text.
const stamped = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.\d+)?Z ([\s\S]*)$/

function splitTime(text: string): { time?: string; text: string } {
  const m = stamped.exec(text)
  if (!m) return { text }
  const d = new Date(m[1] + 'Z')
  const time = d.toLocaleString('fr-FR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
  return { time, text: m[2] }
}

const RECONNECT_MS = 2000

/**
 * Live output of a container, through server-sent events. While "live" is on, the stream comes back by
 * itself when it ends (redeploy, crash, restart, network); off, the view freezes.
 */
export function Logs({ url }: { url: string }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [live, setLive] = useState(true)
  const [connected, setConnected] = useState(false)
  const box = useRef<HTMLPreElement>(null)
  const stick = useRef(true)

  useEffect(() => {
    if (!live) return
    let source: EventSource | null = null
    let retry: ReturnType<typeof setTimeout> | undefined
    let stopped = false

    const connect = () => {
      // Each connection replays the last lines, so the view restarts from them instead of duplicating.
      source = new EventSource(`${url}?tail=300`)
      let fresh = true
      source.onopen = () => setConnected(true)
      source.onmessage = (e) => {
        const line = JSON.parse(e.data) as LogLine & { end?: boolean }
        // The end of a stream may carry why (e.g. no container yet): show it, then reconnect.
        if (!line.end || line.text) {
          setLines((prev) => {
            const base = fresh ? [] : prev
            fresh = false
            return [...base.slice(-1999), { ...splitTime(line.text), stderr: line.stderr || line.end }]
          })
        }
        if (line.end) reconnect()
      }
      source.onerror = reconnect
    }
    const reconnect = () => {
      source?.close()
      setConnected(false)
      if (!stopped) retry = setTimeout(connect, RECONNECT_MS)
    }

    connect()
    return () => {
      stopped = true
      clearTimeout(retry)
      source?.close()
      setConnected(false)
    }
  }, [url, live])

  useEffect(() => {
    if (stick.current && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [lines])

  return (
    <section className="logs-panel">
      <div className="logs-head">
        {live ? (
          <span className="live-status">
            <span className={`dot ${connected ? 'dot-up' : 'dot-warn'}`} />
            {connected ? 'En direct' : 'Reconnexion…'}
          </span>
        ) : (
          <span className="live-status">En pause</span>
        )}
        <span className="spacer" />
        <label className="toggle">
          <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
          En direct
        </label>
      </div>
      <pre
        className="logs"
        ref={box}
        onScroll={(e) => {
          const el = e.currentTarget
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
        }}
      >
        {lines.length === 0 ? (
          <span className="log-muted">Aucune ligne pour le moment.</span>
        ) : (
          lines.map((l, i) => (
            <span key={i} className={l.stderr ? 'log-err' : undefined}>
              {l.time && <span className="log-time">{l.time} </span>}
              {l.text}
              {'\n'}
            </span>
          ))
        )}
      </pre>
    </section>
  )
}

/** Where an app's logo comes from: Docker Hub, an uploaded picture, or its initial on a colour. */
function LogoChooser({ app, onClose, onSaved }: { app: App; onClose: () => void; onSaved: () => void }) {
  const [mode, setMode] = useState(app.logo.mode)
  const [color, setColor] = useState(app.logo.color ?? '')
  const [picture, setPicture] = useState('') // a newly framed picture, as a data URL
  const [cropping, setCropping] = useState<File | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const file = useRef<HTMLInputElement>(null)
  const hasPicture = picture !== '' || (app.logo.mode === 'custom' && !!app.logo.url)

  async function save() {
    setBusy(true)
    setError('')
    try {
      await api.setAppLogo(app.id, { mode, color: mode === 'initial' ? color : '', image: mode === 'custom' && picture ? picture : undefined })
      onSaved()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  const option = (value: App['logo']['mode'], preview: ReactNode, title: string, text: ReactNode, extra?: ReactNode) => (
    <label className={`logo-option ${mode === value ? 'selected' : ''}`}>
      <input type="radio" name="logo" checked={mode === value} onChange={() => (value === 'custom' && !hasPicture ? file.current?.click() : setMode(value))} />
      {preview}
      <span className="logo-option-text">
        <strong>{title}</strong>
        <small className="muted">{text}</small>
        {extra}
      </span>
    </label>
  )

  return (
    <>
      <Modal
        title={`Logo de « ${app.name} »`}
        onClose={onClose}
        footer={
          <>
            <button type="button" className="btn" onClick={onClose} disabled={busy}>
              Annuler
            </button>
            <button type="button" className="btn btn-primary" onClick={save} disabled={busy || (mode === 'custom' && !hasPicture)}>
              Enregistrer
            </button>
          </>
        }
      >
        {option(
          'auto',
          <AppLogo url={app.logo.autoUrl} name={app.name} size={48} />,
          'Automatique',
          app.logo.autoUrl ? <>Le logo de l’image sur Docker Hub, ou celui de son éditeur. Sans logo trouvé, l’initiale.</> : <>Cette image ne vient pas de Docker Hub : l’initiale est utilisée.</>,
        )}
        {option(
          'custom',
          picture ? (
            <img className="app-logo app-logo-preview" src={picture} alt="" />
          ) : app.logo.mode === 'custom' && app.logo.url ? (
            <AppLogo url={app.logo.url} name={app.name} size={48} />
          ) : (
            <span className="app-logo app-logo-upload" aria-hidden="true">
              ↑
            </span>
          ),
          'Mon image',
          'PNG, JPEG, WebP ou GIF, cadrée en carré.',
          hasPicture && (
            <button type="button" className="link-btn" onClick={() => file.current?.click()}>
              Choisir une autre image
            </button>
          ),
        )}
        {option(
          'initial',
          <AppLogo name={app.name} color={color || undefined} size={48} />,
          'Initiale et couleur',
          'La première lettre du nom sur une couleur.',
          <span className="color-swatches" role="radiogroup" aria-label="Couleur">
            {LOGO_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={color === c}
                aria-label={c}
                className={`swatch ${color === c ? 'on' : ''}`}
                style={{ background: c }}
                onClick={() => {
                  setColor(c)
                  setMode('initial')
                }}
              />
            ))}
          </span>,
        )}
        <input
          ref={file}
          type="file"
          accept="image/png,image/jpeg,image/webp,image/gif"
          hidden
          onChange={(e) => {
            const f = e.target.files?.[0]
            e.target.value = ''
            if (f) setCropping(f)
          }}
        />
        {error && <p className="error">{error}</p>}
      </Modal>
      {cropping && (
        <ImageCropper
          file={cropping}
          title="Cadrer le logo"
          outWidth={256}
          outHeight={256}
          onCancel={() => setCropping(null)}
          onSave={async (dataUrl) => {
            setPicture(dataUrl)
            setMode('custom')
            setCropping(null)
          }}
        />
      )}
    </>
  )
}

/** Admins: move an app to another node, without a gap when it runs. */
function MoveApp({ app, onClose, onMoved }: { app: App; onClose: () => void; onMoved: () => void }) {
  const [choices, setChoices] = useState<NodeChoice[] | null>(null)
  const [nodeId, setNodeId] = useState(0)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.nodeChoices().then((c) => {
      const others = c.filter((x) => x.id !== app.nodeId)
      setChoices(others)
      setNodeId(others[0]?.id ?? 0)
    }, (err) => setError(errorMessage(err)))
  }, [app.nodeId])

  // The data of an app's volumes stays on its node: a move needs saying so.
  const hasData = app.volumes.length > 0
  const [leaveData, setLeaveData] = useState(false)

  async function move() {
    setBusy(true)
    setError('')
    try {
      await api.moveApp(app.id, nodeId, leaveData)
      onMoved()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      title={`Déplacer « ${app.name} »`}
      subtitle={`Aujourd’hui sur ${app.nodeName}.`}
      onClose={onClose}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="button" className="btn btn-primary" onClick={move} disabled={busy || !nodeId || (hasData && !leaveData)}>
            Déplacer
          </button>
        </>
      }
    >
      {choices && choices.length === 0 ? (
        <p className="muted">Aucun autre node en ligne.</p>
      ) : (
        <label className="field">
          <span>Vers</span>
          <select value={nodeId} onChange={(e) => setNodeId(Number(e.target.value))}>
            {(choices ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {nodeChoiceLabel(c)}
              </option>
            ))}
          </select>
        </label>
      )}
      <p className="muted">
        L’app démarre sur le nouveau node pendant que l’ancien continue de la servir ; l’ancien l’arrête dès qu’elle est en ligne. Si les
        deux nodes n’ont pas la même IP publique, son DNS change et l’ancien la garde encore quelques minutes. Les données écrites dans le
        conteneur ne suivent pas.
      </p>
      {hasData && (
        <label className="check-line warn-box">
          <input type="checkbox" checked={leaveData} onChange={(e) => setLeaveData(e.target.checked)} />
          <span>
            L’app repart <strong>sans ses données</strong> sur le nouveau node : ses volumes ({app.volumes.map((v) => v.path).join(', ')}) restent sur{' '}
            {app.nodeName}, où ils sont gardés jusqu’à la suppression de l’app.
          </span>
        </label>
      )}
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

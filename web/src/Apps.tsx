import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { api, errorMessage, type App, type AppEvent, type AppInput, type AppState, type AppUsage, type Container, type Node } from './api'
import { AppLogo, AreaChart, formatBytes, LOGO_COLORS, Modal, since, Switch, upFor } from './ui'
import { ImageCropper } from './Cropper'

const POLL_MS = 3000
const USAGE_POLL_MS = 15000

const stateLabels: Record<AppState, { label: string; tone: 'up' | 'warn' | 'down' | '' }> = {
  pending: { label: 'En attente', tone: '' },
  pulling: { label: 'Téléchargement de l’image…', tone: 'warn' },
  creating: { label: 'Démarrage…', tone: 'warn' },
  running: { label: 'En ligne', tone: 'up' },
  restarting: { label: 'Redémarre en boucle', tone: 'down' },
  stopped: { label: 'Arrêtée', tone: '' },
  exited: { label: 'Plantée', tone: 'down' },
  error: { label: 'Erreur', tone: 'down' },
  'node-offline': { label: 'Node hors ligne', tone: 'down' },
}

function stateText(a: App): string {
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
    return <AppDetail app={app} onChange={load} go={go} />
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
      const tone = stateLabels[a.state]?.tone ?? ''
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
  const [kind, setKind] = useState<Kind>('all')
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
      submitLabel="Déployer"
      onClose={() => setCreating(false)}
      onSubmit={async (input) => {
        const app = await api.createApp(input)
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
              <LauncherIcon key={i.key} item={i} />
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
                <AppTile key={i.key} item={i} />
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
              <AppCard key={i.key} item={i} admin={admin} />
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

/** View B: logo, name, owner, state and address. */
function AppCard({ item: i, admin }: { item: Item; admin: boolean }) {
  const address = i.app?.url?.replace(/^https?:\/\//, '') ?? (i.container?.ports.length ? i.container.ports.join(' · ') : '')
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

function AppFormModal({
  title,
  submitLabel,
  initial,
  onSubmit,
  onClose,
}: {
  title: string
  submitLabel: string
  initial?: App
  onSubmit: (input: AppInput) => Promise<void>
  onClose: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [image, setImage] = useState(initial?.image ?? '')
  const [port, setPort] = useState(String(initial?.port ?? 80))
  const [memory, setMemory] = useState(String(initial?.memoryMb ?? 512))
  const [env, setEnv] = useState<{ key: string; value: string }[]>(Object.entries(initial?.env ?? {}).map(([key, value]) => ({ key, value })))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const vars: Record<string, string> = {}
      for (const { key, value } of env) if (key.trim()) vars[key.trim()] = value
      await onSubmit({ name: initial ? undefined : name, image, port: Number(port), memoryMb: Number(memory), env: vars })
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      title={title}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? 'Envoi…' : submitLabel}
          </button>
        </>
      }
    >
      {!initial && (
        <label className="field">
          <span>Nom</span>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="mon-blog" pattern="[a-z0-9]([a-z0-9\-]{0,30}[a-z0-9])?" title="a-z, 0-9 et -, sans tiret au début ni à la fin" required autoFocus />
          <small>Il devient aussi le sous-domaine de l’app.</small>
        </label>
      )}
      <label className="field">
        <span>Image Docker</span>
        <input value={image} onChange={(e) => setImage(e.target.value)} placeholder="nginx:alpine" required autoFocus={!!initial} />
      </label>
      <div className="form-row">
        <label className="field">
          <span>Port de l’app</span>
          <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
          <small>Celui sur lequel l’app écoute dans son conteneur.</small>
        </label>
        <label className="field">
          <span>Mémoire max. (Mo)</span>
          <input type="number" min={64} max={16384} step={64} value={memory} onChange={(e) => setMemory(e.target.value)} required />
        </label>
      </div>
      <div className="field">
        <span>Variables d’environnement</span>
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
          <button type="button" className="btn btn-dashed" onClick={() => setEnv([...env, { key: '', value: '' }])}>
            + Ajouter une variable
          </button>
        </div>
        <small>Chiffrées avant d’être enregistrées.</small>
      </div>
      {initial && <p className="muted">Enregistrer recrée le conteneur : l’app est coupée quelques secondes.</p>}
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

type DetailTab = 'observability' | 'logs' | 'events'

function AppDetail({ app, onChange, go }: { app: App; onChange: () => void; go: (path: string) => void }) {
  const [full, setFull] = useState<App | null>(null)
  const [editing, setEditing] = useState(false)
  const [tab, setTab] = useState<DetailTab>('observability')
  const [choosingLogo, setChoosingLogo] = useState(false)
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

  const tone = stateLabels[app.state]?.tone ?? ''

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

        {app.url ? (
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
          <dt>Node</dt>
          <dd>{app.nodeName}</dd>
          <dt>Port</dt>
          <dd>{app.port}</dd>
          <dt>Mémoire</dt>
          <dd>{app.memoryMb} Mo max.</dd>
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
        <div className="detail-actions">
          <button type="button" className="btn" disabled={busy || app.suspended} onClick={() => run(() => api.appAction(app.id, 'redeploy'))}>
            Redéployer
          </button>
          {app.running ? (
            <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.appAction(app.id, 'stop'))}>
              Arrêter
            </button>
          ) : (
            <button type="button" className="btn" disabled={busy || app.suspended} onClick={() => run(() => api.appAction(app.id, 'start'))}>
              Démarrer
            </button>
          )}
          <button type="button" className="btn" disabled={busy || !full} onClick={() => setEditing(true)}>
            Configuration
          </button>
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy}
            onClick={() => {
              if (window.confirm(`Supprimer l’app « ${app.name} » ? Son conteneur et son enregistrement DNS seront supprimés.`)) {
                run(async () => {
                  await api.deleteApp(app.id)
                  go('apps')
                })
              }
            }}
          >
            Supprimer
          </button>
        </div>
      </aside>

      <main className="detail-main">
        <DetailTabs tab={tab} onTab={setTab} label="Vues de l’app" />
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
        {tab === 'events' && <Events eventsKey={`app-${app.id}`} load={() => api.appEvents(app.id)} />}
      </main>

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

function DetailTabs({ tab, onTab, label }: { tab: DetailTab; onTab: (t: DetailTab) => void; label: string }) {
  return (
    <div className="segmented" role="tablist" aria-label={label}>
      {(
        [
          ['observability', 'Observabilité'],
          ['logs', 'Logs'],
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
        <DetailTabs tab={tab} onTab={setTab} label="Vues du conteneur" />
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
        {tab === 'events' && <Events eventsKey={`c-${c.nodeId}-${c.name}`} load={() => api.containerEvents(c)} />}
      </main>
    </div>
  )
}

type LogLine = { text: string; stderr?: boolean }

const RECONNECT_MS = 2000

/**
 * Live output of a container, through server-sent events. While "live" is on, the stream comes back by
 * itself when it ends (redeploy, crash, restart, network); off, the view freezes.
 */
function Logs({ url }: { url: string }) {
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
            return [...base.slice(-1999), { text: line.text, stderr: line.stderr || line.end }]
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

import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, errorMessage, type App, type AppEvent, type AppInput, type AppState, type AppUsage, type Container, type Node } from './api'
import { AreaChart, formatBytes, Modal, since } from './ui'

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
  route,
  go,
}: {
  admin: boolean
  superadmin: boolean
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
  return <AppList apps={apps} nodes={nodes} containers={containers} admin={admin} error={error} onChange={load} go={go} />
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

function AppList({
  apps,
  nodes,
  containers,
  admin,
  error,
  onChange,
  go,
}: {
  apps: App[] | null
  nodes: Node[]
  containers: Container[]
  admin: boolean
  error: string
  onChange: () => void
  go: (path: string) => void
}) {
  const [creating, setCreating] = useState(false)
  const [query, setQuery] = useState('')

  const q = query.trim().toLowerCase()
  const match = (...fields: string[]) => !q || fields.some((f) => f.toLowerCase().includes(q))
  const shownApps = (apps ?? []).filter((a) => match(a.name, a.image, a.ownerName))
  const shownContainers = containers.filter((c) => match(c.name, c.image, c.ownerName))
  const online = apps?.filter((a) => a.state === 'running').length ?? 0
  const failing = apps?.filter((a) => ['exited', 'restarting', 'error'].includes(a.state)) ?? []

  // Admins see one column per machine; the node of an app is never a user's concern.
  const columns = nodes.filter((n) => n.state !== 'pending' || shownApps.some((a) => a.nodeId === n.id))

  return (
    <div className="section">
      <div className="toolbar">
        <h1 className="toolbar-title">
          Apps{' '}
          <span className="muted">
            {online} en ligne sur {apps?.length ?? 0}
          </span>
        </h1>
        <label className="search">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={admin ? 'App, image, propriétaire…' : 'Rechercher une app'} aria-label="Rechercher" />
        </label>
        <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
          + Nouvelle app
        </button>
      </div>

      {failing.length > 0 && (
        <div className="banner banner-down" role="status">
          <span className="dot dot-down" />
          <span>
            {failing.map((a, i) => (
              <span key={a.id}>
                {i > 0 && ', '}
                <a href={`#/apps/${a.id}`}>
                  <b>{a.name}</b>
                </a>
              </span>
            ))}{' '}
            {failing.length > 1 ? 'ont un problème' : 'a un problème'}.
          </span>
        </div>
      )}

      {error && <p className="error">{error}</p>}

      {apps && apps.length === 0 && containers.length === 0 && (
        <div className="empty-state">
          <strong>Aucune app pour le moment</strong>
          <span>Déployez une image Docker, par exemple nginx:alpine sur le port 80.</span>
        </div>
      )}

      {admin && columns.length > 0 && (
        <div className="node-columns">
          {columns.map((n) => {
            const nodeApps = shownApps.filter((a) => a.nodeId === n.id)
            const nodeContainers = shownContainers.filter((c) => c.nodeId === n.id)
            return (
              <section key={n.id} className="node-column" aria-label={`Node ${n.name}`}>
                <div className="node-column-head">
                  <Dot tone={n.state === 'online' ? 'up' : 'down'} />
                  <h2>{n.name}</h2>
                  <span className="muted">
                    {n.state === 'online'
                      ? `${nodeApps.length} app${nodeApps.length > 1 ? 's' : ''}${nodeContainers.length ? ` · ${nodeContainers.length} externe${nodeContainers.length > 1 ? 's' : ''}` : ''}`
                      : n.state === 'offline'
                        ? 'hors ligne'
                        : 'en attente'}
                  </span>
                </div>
                {n.metrics && (
                  <div className="mini-meters">
                    <MiniMeter label="CPU" text={`${n.metrics.cpuPercent.toFixed(0)} %`} pct={n.metrics.cpuPercent} />
                    <MiniMeter label="RAM" text={`${formatBytes(n.metrics.memoryUsedBytes)} / ${formatBytes(n.memoryBytes)}`} pct={(n.metrics.memoryUsedBytes / (n.memoryBytes || 1)) * 100} />
                  </div>
                )}
                {nodeApps.map((a) => (
                  <AppCard key={a.id} app={a} admin />
                ))}
                {nodeContainers.map((c) => (
                  <ContainerCard key={c.id} container={c} />
                ))}
                {nodeApps.length === 0 && nodeContainers.length === 0 && <div className="column-empty">{q ? 'Rien ne correspond' : 'Aucune app'}</div>}
              </section>
            )
          })}
        </div>
      )}

      {!admin && shownApps.length > 0 && (
        <div className="card-grid">
          {shownApps.map((a) => (
            <AppCard key={a.id} app={a} admin={false} />
          ))}
        </div>
      )}

      {creating && (
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
      )}
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

function AppCard({ app, admin }: { app: App; admin: boolean }) {
  const tone = stateLabels[app.state]?.tone ?? ''
  return (
    <a href={`#/apps/${app.id}`} className={`app-card ${tone === 'down' ? 'app-card-down' : ''}`}>
      <span className="app-card-head">
        <strong>{app.name}</strong>
        <span className="app-card-state">
          <Dot tone={tone} />
          {stateText(app)}
        </span>
      </span>
      {app.url && <span className="app-card-url">{app.url.replace(/^https?:\/\//, '')}</span>}
      <span className="muted app-card-meta">
        {app.image}
        {admin && ` · ${app.ownerName}`}
      </span>
    </a>
  )
}

function ContainerCard({ container: c }: { container: Container }) {
  const tone = containerTones[c.state] ?? ''
  return (
    <a href={`#/containers/${c.nodeId}/${c.id}`} className="app-card app-card-external">
      <span className="app-card-head">
        <strong>{c.name}</strong>
        <span className="badge">externe</span>
        <span className="app-card-state">
          <Dot tone={tone} />
          {containerLabels[c.state] ?? c.state}
        </span>
      </span>
      {c.ports.length > 0 && <span className="app-card-url">{c.ports.join(' · ')}</span>}
      <span className="muted app-card-meta">
        {c.image} · {c.ownerName}
      </span>
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
          <span className="app-icon app-icon-lg" aria-hidden="true">
            {app.name.charAt(0).toUpperCase()}
          </span>
          <div>
            <h1>{app.name}</h1>
            <span className={`state-line state-${tone}`}>
              <Dot tone={tone} />
              {stateText(app)}
              {app.state === 'running' && app.startedAt ? ` depuis ${since(app.startedAt).replace('il y a ', '')}` : ''}
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
          <dd>{app.ownerName}</dd>
        </dl>

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
        <div className="segmented" role="tablist" aria-label="Vues de l’app">
          {(
            [
              ['observability', 'Observabilité'],
              ['logs', 'Logs'],
              ['events', 'Événements'],
            ] as const
          ).map(([value, label]) => (
            <button key={value} type="button" role="tab" aria-selected={tab === value} className={tab === value ? 'on' : ''} onClick={() => setTab(value)}>
              {label}
            </button>
          ))}
        </div>
        {tab === 'observability' && <Observability app={app} />}
        {tab === 'logs' && <Logs url={`/api/apps/${app.id}/logs`} />}
        {tab === 'events' && <Events appId={app.id} />}
      </main>

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

function Observability({ app }: { app: App }) {
  const [usage, setUsage] = useState<AppUsage | null>(null)

  useEffect(() => {
    const load = () => api.appUsage(app.id).then(setUsage, () => {})
    load()
    const id = setInterval(load, USAGE_POLL_MS)
    return () => clearInterval(id)
  }, [app.id])

  const running = app.state === 'running'
  const limit = usage?.memoryLimitBytes ?? app.memoryMb << 20
  const samples = usage?.samples ?? []

  return (
    <>
      <div className="tiles">
        <div className="tile">
          <div className="k">CPU</div>
          <div className="v">{running ? `${(app.cpuPercent ?? 0).toFixed(1)} %` : '–'}</div>
        </div>
        <div className="tile">
          <div className="k">Mémoire</div>
          <div className="v">
            {running && app.memoryUsedBytes ? formatBytes(app.memoryUsedBytes) : '–'} <small>/ {formatBytes(limit)}</small>
          </div>
        </div>
        <div className="tile">
          <div className="k">Redémarrages</div>
          <div className={`v ${app.restartCount ? 'v-down' : ''}`}>{app.restartCount ?? 0}</div>
        </div>
        <div className="tile">
          <div className="k">Démarrée</div>
          <div className="v v-small">{running && app.startedAt ? since(app.startedAt) : '–'}</div>
        </div>
      </div>
      <section className="panel chart-panel">
        <div className="chart-head">
          <h2>CPU</h2>
          <span className="muted">dernière heure · en % d’un cœur</span>
        </div>
        <AreaChart values={samples.map((s) => s.cpu)} max={5} label="CPU sur la dernière heure" />
      </section>
      <section className="panel chart-panel">
        <div className="chart-head">
          <h2>Mémoire</h2>
          <span className="muted">dernière heure · limite {formatBytes(limit)}</span>
        </div>
        <AreaChart values={samples.map((s) => s.mem)} max={limit} color="var(--warn)" label="Mémoire sur la dernière heure" />
      </section>
    </>
  )
}

function Events({ appId }: { appId: number }) {
  const [events, setEvents] = useState<AppEvent[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const load = () =>
      api.appEvents(appId).then(
        (e) => {
          setEvents(e)
          setError('')
        },
        (err) => setError(errorMessage(err)),
      )
    load()
    const id = setInterval(load, POLL_MS * 2)
    return () => clearInterval(id)
  }, [appId])

  if (error) return <p className="error">{error}</p>
  if (!events) return null
  if (events.length === 0) return <div className="empty-state">Aucun événement pour le moment.</div>
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

function ContainerDetail({ container: c, superadmin, onChange, go }: { container: Container; superadmin: boolean; onChange: () => void; go: (path: string) => void }) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const tone = containerTones[c.state] ?? ''

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
          <span className="app-icon app-icon-lg app-icon-external" aria-hidden="true">
            {c.name.charAt(0).toUpperCase()}
          </span>
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
          <dt>CPU</dt>
          <dd>{c.state === 'running' ? `${(c.cpuPercent ?? 0).toFixed(1)} %` : '–'}</dd>
          <dt>Mémoire</dt>
          <dd>{c.memoryUsedBytes ? formatBytes(c.memoryUsedBytes) : '–'}</dd>
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
        {superadmin ? (
          <Logs url={`/api/admin/nodes/${c.nodeId}/containers/${c.id}/logs`} />
        ) : (
          <div className="empty-state">Les logs des conteneurs externes sont réservés au superadmin.</div>
        )}
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

import { useEffect, useRef, useState, type FormEvent } from 'react'
import { api, errorMessage, type App, type AppInput, type AppState, type Node } from './api'

const POLL_MS = 3000

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
  return base
}

function StateDot({ state }: { state: AppState }) {
  const tone = stateLabels[state]?.tone
  return <span className={`dot ${tone ? `dot-${tone}` : ''}`} />
}

export default function Apps({ admin }: { admin: boolean }) {
  const [apps, setApps] = useState<App[] | null>(null)
  const [nodes, setNodes] = useState<Node[] | null>(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)

  async function load() {
    try {
      setApps(await api.apps())
      if (admin) setNodes(await api.nodes())
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

  const current = apps?.find((a) => a.id === selected)
  if (selected !== null && current) {
    return <AppDetail app={current} onBack={() => setSelected(null)} onChange={load} />
  }

  const online = apps?.filter((a) => a.state === 'running').length ?? 0
  const failing = apps?.filter((a) => ['exited', 'restarting', 'error'].includes(a.state)) ?? []

  return (
    <div className="section">
      <div className="stats">
        <div className="stat">
          <div className="k">Apps en ligne</div>
          <div className="v">
            {online} <small>/ {apps?.length ?? 0}</small>
          </div>
        </div>
        <div className="stat">
          <div className="k">En erreur</div>
          <div className={`v ${failing.length ? 'v-down' : ''}`}>{failing.length}</div>
        </div>
        {admin && (
          <div className="stat">
            <div className="k">Nodes en ligne</div>
            <div className="v">
              {nodes ? (
                <>
                  {nodes.filter((n) => n.state === 'online').length} <small>/ {nodes.filter((n) => n.state !== 'pending').length}</small>
                </>
              ) : (
                '–'
              )}
            </div>
          </div>
        )}
      </div>

      {failing.length > 0 && (
        <div className="banner banner-down" role="status">
          <span className="dot dot-down" />
          <span>
            {failing.map((a, i) => (
              <span key={a.id}>
                {i > 0 && ', '}
                <b>{a.name}</b>
              </span>
            ))}{' '}
            {failing.length > 1 ? 'ont un problème' : 'a un problème'}.
          </span>
        </div>
      )}

      <div className="header">
        <span className="spacer" />
        {!creating && (
          <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
            + Nouvelle app
          </button>
        )}
      </div>

      {creating && (
        <AppForm
          title="Nouvelle app"
          submitLabel="Déployer"
          onCancel={() => setCreating(false)}
          onSubmit={async (input) => {
            const app = await api.createApp(input)
            setCreating(false)
            await load()
            setSelected(app.id)
          }}
        />
      )}

      {error && <p className="error">{error}</p>}

      {apps && apps.length === 0 && !creating && (
        <div className="empty-state">
          <strong>Aucune app pour le moment</strong>
          <span>Déployez une image Docker, par exemple nginx:alpine sur le port 80.</span>
        </div>
      )}

      {apps && apps.length > 0 && (
        <div className="table">
          <div className="row head">
            <span />
            <span>App</span>
            <span>Adresse</span>
            <span>Statut</span>
            <span>{admin ? 'Node' : 'Image'}</span>
          </div>
          {apps.map((a) => (
            <button key={a.id} type="button" className="row" onClick={() => setSelected(a.id)}>
              <span className="app-icon" aria-hidden="true">
                {a.name.charAt(0).toUpperCase()}
              </span>
              <span className="cell-main">
                <strong>{a.name}</strong>
                {admin && <small className="muted"> · {a.ownerName}</small>}
              </span>
              <span className="muted cell-ellipsis">{a.url ?? '—'}</span>
              <span className="cell-state">
                <StateDot state={a.state} />
                {stateText(a)}
              </span>
              <span className="muted cell-ellipsis">{admin ? a.nodeName : a.image}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function AppForm({
  title,
  submitLabel,
  initial,
  onSubmit,
  onCancel,
}: {
  title: string
  submitLabel: string
  initial?: App
  onSubmit: (input: AppInput) => Promise<void>
  onCancel?: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [image, setImage] = useState(initial?.image ?? '')
  const [port, setPort] = useState(String(initial?.port ?? 80))
  const [memory, setMemory] = useState(String(initial?.memoryMb ?? 512))
  const [env, setEnv] = useState<{ key: string; value: string }[]>(
    Object.entries(initial?.env ?? {}).map(([key, value]) => ({ key, value })),
  )
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
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel" onSubmit={submit}>
      <h2>{title}</h2>
      <div className="form-grid">
        {!initial && (
          <label className="field">
            <span>Nom</span>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="mon-blog" pattern="[a-z0-9]([a-z0-9\-]{0,30}[a-z0-9])?" title="a-z, 0-9 et -, sans tiret au début ni à la fin" required autoFocus />
            <small>Il devient aussi le sous-domaine de l’app.</small>
          </label>
        )}
        <label className="field">
          <span>Image Docker</span>
          <input value={image} onChange={(e) => setImage(e.target.value)} placeholder="nginx:alpine" required />
        </label>
        <label className="field">
          <span>Port de l’app</span>
          <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
          <small>Celui sur lequel l’app écoute dans son conteneur.</small>
        </label>
        <label className="field">
          <span>Mémoire (Mo)</span>
          <input type="number" min={64} max={16384} step={64} value={memory} onChange={(e) => setMemory(e.target.value)} required />
        </label>
      </div>

      <div className="field">
        <span>Variables d’environnement</span>
        {env.map((v, i) => (
          <div className="env-row" key={i}>
            <input
              aria-label="Nom de la variable"
              value={v.key}
              onChange={(e) => setEnv(env.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)))}
              placeholder="NOM"
            />
            <input
              aria-label="Valeur"
              value={v.value}
              onChange={(e) => setEnv(env.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
              placeholder="valeur"
            />
            <button type="button" className="btn btn-ghost" onClick={() => setEnv(env.filter((_, j) => j !== i))} aria-label="Retirer la variable">
              ✕
            </button>
          </div>
        ))}
        <div>
          <button type="button" className="btn" onClick={() => setEnv([...env, { key: '', value: '' }])}>
            + Ajouter une variable
          </button>
        </div>
        <small>Chiffrées avant d’être enregistrées.</small>
      </div>

      {error && <p className="error">{error}</p>}
      <div className="panel-footer">
        <span className="spacer" />
        {onCancel && (
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            Annuler
          </button>
        )}
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Envoi…' : submitLabel}
        </button>
      </div>
    </form>
  )
}

function AppDetail({ app, onBack, onChange }: { app: App; onBack: () => void; onChange: () => void }) {
  const [full, setFull] = useState<App | null>(null)
  const [editing, setEditing] = useState(false)
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

  return (
    <div className="section">
      <div className="header">
        <button type="button" className="btn btn-ghost" onClick={onBack}>
          ← Apps
        </button>
      </div>

      <div className="panel">
        <div className="header">
          <span className="app-icon app-icon-lg" aria-hidden="true">
            {app.name.charAt(0).toUpperCase()}
          </span>
          <div className="header-title">
            <h2>{app.name}</h2>
            <p className="muted">
              {app.image} · port {app.port} · {app.memoryMb} Mo · node {app.nodeName}
            </p>
          </div>
          <span className={`banner ${stateLabels[app.state]?.tone ? `banner-${stateLabels[app.state].tone}` : ''}`}>
            <StateDot state={app.state} />
            {stateText(app)}
          </span>
        </div>
        {app.url && (
          <p>
            <a href={app.url} target="_blank" rel="noopener noreferrer">
              {app.url}
            </a>
          </p>
        )}
        {app.error && <p className="error">{app.error}</p>}
        {error && <p className="error">{error}</p>}
        <div className="panel-footer">
          <button type="button" className="btn btn-primary" disabled={busy} onClick={() => run(() => api.appAction(app.id, 'redeploy'))}>
            Redéployer
          </button>
          {app.running ? (
            <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.appAction(app.id, 'stop'))}>
              Arrêter
            </button>
          ) : (
            <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.appAction(app.id, 'start'))}>
              Démarrer
            </button>
          )}
          <button type="button" className="btn" disabled={busy || !full} onClick={() => setEditing(!editing)}>
            {editing ? 'Fermer la configuration' : 'Configuration'}
          </button>
          <span className="spacer" />
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy}
            onClick={() => {
              if (window.confirm(`Supprimer l’app « ${app.name} » ? Son conteneur et son enregistrement DNS seront supprimés.`)) {
                run(async () => {
                  await api.deleteApp(app.id)
                  onBack()
                })
              }
            }}
          >
            Supprimer
          </button>
        </div>
      </div>

      {editing && full && (
        <AppForm
          title="Configuration"
          submitLabel="Enregistrer et redéployer"
          initial={full}
          onCancel={() => setEditing(false)}
          onSubmit={async (input) => {
            setFull(await api.updateApp(app.id, input))
            setEditing(false)
            onChange()
          }}
        />
      )}

      <Logs appId={app.id} />
    </div>
  )
}

type LogLine = { text: string; stderr?: boolean }

/** Live output of the app's container, through server-sent events. */
function Logs({ appId }: { appId: number }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [status, setStatus] = useState('Connexion…')
  const [generation, setGeneration] = useState(0)
  const box = useRef<HTMLPreElement>(null)
  const stick = useRef(true)

  useEffect(() => {
    setLines([])
    setStatus('Connexion…')
    const source = new EventSource(`/api/apps/${appId}/logs?tail=300`)
    source.onopen = () => setStatus('')
    source.onmessage = (e) => {
      const line = JSON.parse(e.data) as LogLine & { end?: boolean }
      if (line.end) {
        if (line.text) setLines((prev) => [...prev, { text: line.text, stderr: true }])
        setStatus('Flux terminé')
        source.close()
        return
      }
      setLines((prev) => [...prev.slice(-1999), line])
    }
    source.onerror = () => {
      setStatus('Flux interrompu')
      source.close()
    }
    return () => source.close()
  }, [appId, generation])

  useEffect(() => {
    if (stick.current && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [lines])

  return (
    <div className="panel">
      <div className="header">
        <h2 className="header-title">Logs</h2>
        {status && <span className="muted">{status}</span>}
        <button type="button" className="btn" onClick={() => setGeneration((g) => g + 1)}>
          Recharger
        </button>
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
          <span className="muted">Aucune ligne pour le moment.</span>
        ) : (
          lines.map((l, i) => (
            <span key={i} className={l.stderr ? 'log-err' : undefined}>
              {l.text}
              {'\n'}
            </span>
          ))
        )}
      </pre>
    </div>
  )
}

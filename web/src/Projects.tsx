import { useEffect, useRef, useState, type FormEvent } from 'react'
import {
  api,
  ApiError,
  errorMessage,
  type App,
  type BackupPolicy,
  type ComposeError,
  type NodeChoice,
  type Snapshot,
  type Stack,
  type StackPreview,
  type StackState,
  type StackVolume,
} from './api'
import { appTone, Dot, Logs, SourcePick, stateText, type Source } from './Apps'
import { AppLogo, formatBytes, Modal, since, Switch, useRedeploy } from './ui'

export const projectStates: Record<StackState, { label: string; tone: 'up' | 'warn' | 'down' | '' }> = {
  pending: { label: 'En attente', tone: '' },
  running: { label: 'En ligne', tone: 'up' },
  deploying: { label: 'Déploiement…', tone: 'warn' },
  stopped: { label: 'Arrêtée', tone: '' },
  error: { label: 'Un service plante', tone: 'down' },
  blocked: { label: 'Bloquée', tone: 'down' },
  'node-offline': { label: 'Node hors ligne', tone: 'down' },
}

const composeExample = `services:
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_PASSWORD: \${DB_PASSWORD}
    volumes:
      - data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
    x-forgeyard:
      backup: { type: postgres }
  web:
    image: ghcr.io/moi/web:latest
    depends_on:
      db: { condition: service_healthy }
    x-forgeyard:
      expose: { port: 80 }
volumes:
  data:
`

/** A project's picture: the logos of its first services, overlapping. */
export function ProjectLogo({ project, size }: { project: Stack; size: number }) {
  const small = Math.round(size * 0.68)
  return (
    <span className="project-logo" style={{ width: size, height: size }} aria-hidden="true">
      {project.services.slice(0, 2).map((a) => (
        <AppLogo key={a.id} url={a.logo.url} color={a.logo.color} name={a.name} size={small} />
      ))}
      {project.services.length === 0 && <AppLogo name={project.name} size={small} />}
    </span>
  )
}

/** A project in the app list: its state, address, and one line per app. */
export function ProjectCard({ project: p, admin }: { project: Stack; admin: boolean }) {
  const st = projectStates[p.state] ?? { label: p.state, tone: '' }
  return (
    <a href={`#/projects/${p.id}`} className={`app-card project-card ${st.tone === 'down' ? 'app-card-down' : ''}`}>
      <span className="app-card-head">
        <ProjectLogo project={p} size={44} />
        <span className="app-card-title">
          <strong>{p.name}</strong>
          <span className="muted">
            projet · {p.services.length} app{p.services.length > 1 ? 's' : ''}
            {admin ? ` · ${p.ownerName}` : ''}
          </span>
        </span>
        <span className="app-card-state">
          <Dot tone={st.tone} />
          {st.label}
        </span>
      </span>
      <span className={p.url ? 'app-card-url' : 'muted app-card-meta'}>{p.url?.replace(/^https?:\/\//, '') ?? 'aucun service exposé'}</span>
      <span className="project-card-services">
        {p.services.slice(0, 5).map((a) => (
          <span key={a.id} className="project-card-service">
            <Dot tone={appTone(a)} />
            <span>
              {a.service} <span className="muted">· {kindLabels[a.kind] ?? a.kind}</span>
            </span>
            <span className="muted">{stateText(a)}</span>
          </span>
        ))}
        {p.services.length > 5 && <span className="muted">et {p.services.length - 5} autres</span>}
      </span>
    </a>
  )
}

/**
 * The variables a compose file uses: ${VAR}, ${VAR:-…} and $VAR, not $$. A variable is required unless every
 * use gives it a default (:-, -) or only tests it (:+, +): without a value, the project is refused.
 */
function usedVariables(compose: string): { name: string; required: boolean }[] {
  const out = new Map<string, boolean>()
  for (const m of compose.matchAll(/(?<!\$)\$(?:\{([A-Za-z_][A-Za-z0-9_]*)(:?[-+])?|([A-Za-z_][A-Za-z0-9_]*))/g)) {
    const name = m[1] ?? m[3]
    out.set(name, (out.get(name) ?? false) || !m[2])
  }
  return [...out].map(([name, required]) => ({ name, required })).sort((x, y) => Number(y.required) - Number(x.required))
}

const kindLabels: Record<string, string> = { web: 'exposé', service: 'privé', job: 'job', sandbox: 'sandbox' }

export function ProjectFormModal({
  stack,
  admin,
  onClose,
  onSaved,
  onSource,
}: {
  stack?: Stack
  admin?: boolean
  onClose: () => void
  onSaved: (s: Stack) => void
  // onSource: another source picked in Nouvelle app, which then shows its own form.
  onSource?: (s: Source) => void
}) {
  const [name, setName] = useState(stack?.name ?? '')
  const [compose, setCompose] = useState(stack?.compose ?? '')
  const [env, setEnv] = useState<{ key: string; value: string }[]>(Object.entries(stack?.env ?? {}).map(([key, value]) => ({ key, value })))
  // A private registry the images come from; the saved password never comes back, so it is kept unless retyped.
  const [registry, setRegistry] = useState({ server: stack?.registry?.server ?? '', username: stack?.registry?.username ?? '', password: '' })
  const [registryTouched, setRegistryTouched] = useState(false)
  const [registryOpen, setRegistryOpen] = useState(!!stack?.registry)
  const editRegistry = (patch: Partial<typeof registry>) => {
    setRegistry({ ...registry, ...patch })
    setRegistryTouched(true)
  }
  const [preview, setPreview] = useState<StackPreview | null>(null)
  const [errors, setErrors] = useState<ComposeError[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [choices, setChoices] = useState<NodeChoice[]>([])
  const [nodeId, setNodeId] = useState(0)
  const [domain, setDomain] = useState('')
  const editor = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    api.instance().then((i) => setDomain(i.appsDomain ?? ''), () => {})
    if (stack || !admin) return
    api.nodeChoices().then((c) => {
      setChoices(c)
      setNodeId(c.find((x) => x.recommended)?.id ?? 0)
    }, () => {})
  }, [admin, stack])

  const vars = Object.fromEntries(env.filter((v) => v.key.trim()).map((v) => [v.key.trim(), v.value]))
  const varsKey = JSON.stringify(vars)
  // Checked as it is typed, once typing pauses.
  useEffect(() => {
    if (!compose.trim()) {
      setPreview(null)
      setErrors([])
      return
    }
    const t = setTimeout(() => {
      api.validateStack({ name: stack?.name ?? (name.trim() || 'projet'), compose, env: vars }).then(
        (p) => {
          setPreview(p)
          setErrors(p.errors)
        },
        () => {},
      )
    }, 400)
    return () => clearTimeout(t)
  }, [compose, varsKey, name])

  const missing = usedVariables(compose).filter((v) => !(v.name in vars))

  function goToLine(line: number) {
    const ta = editor.current
    if (!ta || line < 1) return
    const lines = compose.split('\n')
    const start = lines.slice(0, line - 1).reduce((n, l) => n + l.length + 1, 0)
    ta.focus()
    ta.setSelectionRange(start, start + (lines[line - 1]?.length ?? 0))
    ta.scrollTop = Math.max(0, (line - 4) * 20)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const login = registryTouched || (!stack && registry.server.trim()) ? registry : undefined
      const saved = stack ? await api.updateStack(stack.id, { compose, env: vars, registry: login }) : await api.createStack({ name, compose, env: vars, nodeId: nodeId || undefined, registry: login })
      onSaved(saved)
    } catch (err) {
      if (err instanceof ApiError && err.composeErrors) setErrors(err.composeErrors)
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  const shownName = stack?.name ?? (name.trim() || 'mon-projet')
  const aside = (
    <>
      <span className="aside-label">Aperçu</span>
      {preview?.error && <p className="error">{preview.error}</p>}
      {preview && preview.services.length > 0 ? (
        <ol className="stack-preview">
          {preview.services.map((s) => (
            <li key={s.name}>
              <span className="stack-preview-head">
                <strong>{s.name}</strong>
                <span className={`badge ${s.kind === 'web' ? 'badge-accent' : ''}`}>{s.kind === 'web' ? `exposé :${s.port}` : kindLabels[s.kind]}</span>
                {s.backup && <span className="badge">pg_dump</span>}
              </span>
              <span className="mono muted">{s.image}</span>
              {s.kind === 'web' && domain && <span className="muted">→ {shownName}.{domain}</span>}
              {s.dependsOn.length > 0 && (
                <span className="muted">
                  après{' '}
                  {s.dependsOn
                    .map((d) => `${d.service}${d.condition === 'service_healthy' ? ' (healthy)' : d.condition === 'service_completed_successfully' ? ' (terminé)' : ''}`)
                    .join(', ')}
                </span>
              )}
            </li>
          ))}
        </ol>
      ) : (
        <p className="muted aside-note">Les services apparaissent ici, dans leur ordre de démarrage.</p>
      )}
      {!!preview?.volumes?.length && (
        <dl className="aside-facts">
          <dt>Volumes</dt>
          <dd className="mono">{preview?.volumes.join(', ')}</dd>
        </dl>
      )}
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
      {stack && <p className="muted aside-note">Seuls les services modifiés sont redéployés.</p>}
      <span className="aside-spacer" />
      {error && <p className="error">{error}</p>}
      <button type="submit" className="btn btn-primary btn-block btn-big" disabled={busy || errors.length > 0}>
        {busy ? 'Envoi…' : stack ? 'Enregistrer et déployer' : 'Créer le projet'}
      </button>
      <button type="button" className="link-button aside-cancel" onClick={onClose} disabled={busy}>
        Annuler
      </button>
    </>
  )

  return (
    <Modal title={stack ? `Projet ${stack.name}` : 'Nouvelle app'} onClose={onClose} onSubmit={submit} aside={aside}>
      {onSource && <SourcePick source="compose" onPick={onSource} />}
      {!stack && (
        <label className="field">
          <span>Nom du projet</span>
          <input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="shameless" required autoFocus pattern="[a-z0-9]([a-z0-9\-]{0,30}[a-z0-9])?" />
          <small>Le service exposé prend ce nom (son sous-domaine), les autres « {shownName}-service ».</small>
        </label>
      )}
      <label className="field">
        <span>docker-compose.yml</span>
        <textarea
          ref={editor}
          className="mono compose-editor"
          value={compose}
          onChange={(e) => setCompose(e.target.value)}
          spellCheck={false}
          rows={20}
          required
          placeholder={composeExample}
          aria-invalid={errors.length > 0}
        />
        <small>
          Des images publiées et des volumes nommés, sans build ni ports. Le service à publier porte <span className="mono">x-forgeyard: {'{ expose: { port: 80 } }'}</span>
          ; Docker Compose ignore ce bloc, le fichier tourne aussi en local.
        </small>
      </label>
      {errors.length > 0 && (
        <ul className="compose-errors" aria-live="polite">
          {errors.map((e, i) => (
            <li key={i}>
              {e.line > 0 ? (
                <button type="button" className="link-button" onClick={() => goToLine(e.line)}>
                  ligne {e.line}
                </button>
              ) : null}
              <span>{e.message}</span>
            </li>
          ))}
        </ul>
      )}
      <div className="field">
        <span>
          Variables <span className="muted">· pour ${'{VAR}'}, chiffrées</span>
        </span>
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
          {missing.length > 0 && (
            <span className="chips-row">
              {missing.map((m) => (
                <button
                  key={m.name}
                  type="button"
                  className={`chip mono ${m.required ? 'chip-required' : ''}`}
                  title={m.required ? 'Obligatoire : le compose n’a pas de valeur par défaut' : 'Facultatif : le compose a une valeur par défaut'}
                  onClick={() => setEnv([...env, { key: m.name, value: '' }])}
                >
                  + {m.name}
                </button>
              ))}
            </span>
          )}
          {missing.some((m) => m.required) && <small className="text-down">En rouge : obligatoires, sans valeur par défaut dans le compose.</small>}
          <div>
            <button type="button" className="btn btn-dashed btn-small" onClick={() => setEnv([...env, { key: '', value: '' }])}>
              + Ajouter une variable
            </button>
          </div>
        </div>
      </div>
      <details className="field" open={registryOpen} onToggle={(e) => setRegistryOpen(e.currentTarget.open)}>
        <summary>
          Registry privé <span className="muted">{registry.server ? `· ${registry.server}` : '· images publiques'}</span>
        </summary>
        <div className="env-list">
          <input aria-label="Adresse du registry" className="mono" value={registry.server} onChange={(e) => editRegistry({ server: e.target.value })} placeholder="ghcr.io" />
          <div className="env-row">
            <input aria-label="Identifiant" value={registry.username} onChange={(e) => editRegistry({ username: e.target.value })} placeholder="identifiant" autoComplete="off" />
            <input
              type="password"
              aria-label="Mot de passe ou token"
              value={registry.password}
              onChange={(e) => editRegistry({ password: e.target.value })}
              placeholder={stack?.registry?.passwordSet ? 'inchangé' : 'token'}
              autoComplete="new-password"
            />
          </div>
          <small>
            Pour télécharger les images privées de cette adresse. Un token en lecture seule suffit ; il est chiffré avant d’être enregistré. Videz l’adresse pour le retirer.
          </small>
        </div>
      </details>
    </Modal>
  )
}

type StackTab = 'services' | 'logs' | 'volumes' | 'compose'

export function ProjectDetail({ stack, onChange, go }: { stack: Stack; onChange: () => void; go: (path: string) => void }) {
  const [full, setFull] = useState<Stack | null>(null)
  const [tab, setTab] = useState<StackTab>('services')
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [logsOf, setLogsOf] = useState(stack.services.find((s) => s.kind === 'web')?.id ?? stack.services[0]?.id)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.stack(stack.id).then(setFull).catch((err) => setError(errorMessage(err)))
  }, [stack.id, stack.updatedAt])

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

  const st = projectStates[stack.state] ?? { label: stack.state, tone: '' }
  const running = stack.services.some((s) => s.running)
  const [redeploying, startRedeploy] = useRedeploy(stack.state === 'deploying')
  const failedJob = stack.services.find((s) => s.state === 'failed')

  return (
    <div className="detail">
      <aside className="panel detail-side">
        <a href="#/apps" className="back-link">
          ← Apps
        </a>
        <div className="detail-title">
          <div>
            <h1>{stack.name}</h1>
            <span className={`state-line state-${st.tone}`}>
              <Dot tone={st.tone} />
              {st.label}
            </span>
          </div>
        </div>
        {stack.url ? (
          <a className="btn btn-primary btn-block" href={stack.url} target="_blank" rel="noopener noreferrer">
            Ouvrir {stack.url.replace(/^https?:\/\//, '')}
          </a>
        ) : (
          <p className="hint">Aucun service exposé : ajoutez x-forgeyard: {'{ expose: { port: 80 } }'} au service à publier.</p>
        )}
        {failedJob && (
          <div className="banner banner-down">
            <span className="dot dot-down" />
            <span>
              Le job <strong>{failedJob.service}</strong> a échoué : les services qui l’attendent ne démarrent pas. Voir{' '}
              <button type="button" className="link-button" onClick={() => (setLogsOf(failedJob.id), setTab('logs'))}>
                ses logs
              </button>
              , corrigez, puis redéployez.
            </span>
          </div>
        )}
        <dl className="kv">
          <dt>Node</dt>
          <dd>{stack.nodeName}</dd>
          <dt>Réseau</dt>
          <dd className="mono">forgeyard-stack-{stack.id}</dd>
          <dt>Propriétaire</dt>
          <dd>
            <a href={`#/u/${stack.ownerId}`}>{stack.ownerName}</a>
          </dd>
          <dt>Modifiée</dt>
          <dd>{since(stack.updatedAt)}</dd>
        </dl>
        {error && <p className="error">{error}</p>}
        <div className="detail-actions">
          {running ? (
            <>
              <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.stackAction(stack.id, 'stop'))}>
                Arrêter
              </button>
              <button
                type="button"
                className={`btn ${redeploying ? 'btn-busy' : ''}`}
                aria-busy={redeploying}
                disabled={busy || redeploying}
                title="Recrée tous les services (images téléchargées à nouveau) et relance les jobs"
                onClick={() => {
                  startRedeploy()
                  run(() => api.stackAction(stack.id, 'redeploy'))
                }}
              >
                Redéployer
              </button>
            </>
          ) : (
            <button type="button" className="btn btn-primary detail-actions-wide" disabled={busy} onClick={() => run(() => api.stackAction(stack.id, 'start'))}>
              Démarrer
            </button>
          )}
          <button type="button" className="btn detail-actions-wide" disabled={busy || !full} onClick={() => setEditing(true)}>
            Modifier le compose
          </button>
        </div>
        <button type="button" className="link-button danger-link" disabled={busy} onClick={() => setDeleting(true)}>
          Supprimer le projet…
        </button>
      </aside>

      <main className="detail-main">
        <div className="segmented" role="tablist" aria-label="Vues du projet">
          {(
            [
              ['services', 'Apps du projet'],
              ['logs', 'Logs'],
              ['volumes', 'Volumes et sauvegardes'],
              ['compose', 'Compose'],
            ] as const
          ).map(([value, text]) => (
            <button key={value} type="button" role="tab" aria-selected={tab === value} className={tab === value ? 'on' : ''} onClick={() => setTab(value)}>
              {text}
            </button>
          ))}
        </div>
        {tab === 'services' && <ServiceList services={stack.services} />}
        {tab === 'logs' && (
          <>
            <div className="chips-row" role="group" aria-label="Service">
              {stack.services.map((s) => (
                <button key={s.id} type="button" className={`chip ${logsOf === s.id ? 'on' : ''}`} onClick={() => setLogsOf(s.id)}>
                  <Dot tone={appTone(s)} />
                  {s.service}
                </button>
              ))}
            </div>
            {logsOf ? <Logs key={logsOf} url={`/api/apps/${logsOf}/logs`} /> : null}
          </>
        )}
        {tab === 'volumes' && <VolumeList stack={stack} onChange={onChange} />}
        {tab === 'compose' && full && (
          <div className="panel compose-view">
            <pre className="mono">{full.compose}</pre>
            {full.env && Object.keys(full.env).length > 0 && (
              <p className="muted">
                Variables : <span className="mono">{Object.keys(full.env).join(', ')}</span>
              </p>
            )}
          </div>
        )}
      </main>

      {editing && full && (
        <ProjectFormModal
          stack={full}
          onClose={() => setEditing(false)}
          onSaved={(s) => {
            setFull(s)
            setEditing(false)
            onChange()
          }}
        />
      )}
      {deleting && <DeleteStack stack={stack} onClose={() => setDeleting(false)} onDeleted={() => (onChange(), go('apps'))} />}
    </div>
  )
}

function ServiceList({ services }: { services: App[] }) {
  return (
    <ol className="panel stack-services">
      {services.map((s) => (
        <li key={s.id}>
          <Dot tone={appTone(s)} />
          <span className="stack-service-main">
            <span className="stack-preview-head">
              <a href={`#/apps/${s.id}`}>
                <strong>{s.service}</strong>
              </a>
              <span className={`badge ${s.kind === 'web' ? 'badge-accent' : ''}`}>{s.kind === 'web' ? `exposé :${s.port}` : kindLabels[s.kind]}</span>
            </span>
            <span className="mono muted">{s.image}</span>
            {s.waitingFor && <span className="state-warn">{s.state === 'waiting' ? 'Attend : ' : 'Nouvelle version en attente : '}{s.waitingFor}</span>}
            {s.error && <span className="state-down">{s.error}</span>}
          </span>
          <span className={`stack-service-state state-${appTone(s)}`}>{stateText(s)}</span>
        </li>
      ))}
    </ol>
  )
}

function DeleteStack({ stack, onClose, onDeleted }: { stack: Stack; onClose: () => void; onDeleted: () => void }) {
  const [volumes, setVolumes] = useState(false)
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api.deleteStack(stack.id, volumes)
      onDeleted()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }
  return (
    <Modal
      title={`Supprimer le projet ${stack.name}`}
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="submit" className="btn btn-danger" disabled={busy || (volumes && confirm !== stack.name)}>
            {busy ? 'Suppression…' : 'Supprimer'}
          </button>
        </>
      }
    >
      <p>
        Ses {stack.services.length} apps, leurs conteneurs, leur enregistrement DNS et le réseau du projet sont supprimés. Les sauvegardes
        déjà faites restent sur le stockage.
      </p>
      {stack.volumes.length > 0 && (
        <>
          <label className="check">
            <input type="checkbox" checked={volumes} onChange={(e) => setVolumes(e.target.checked)} />
            <span>
              Supprimer aussi ses volumes et leurs données
              <small>{stack.volumes.map((v) => v.name).join(', ')}</small>
            </span>
          </label>
          {volumes ? (
            <label className="field">
              <span>Tapez « {stack.name} » pour confirmer</span>
              <input value={confirm} onChange={(e) => setConfirm(e.target.value)} autoFocus />
            </label>
          ) : (
            <p className="muted">Sans cocher, les volumes restent sur le node {stack.nodeName}.</p>
          )}
        </>
      )}
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

function VolumeList({ stack, onChange }: { stack: Stack; onChange: () => void }) {
  if (stack.volumes.length === 0)
    return <div className="empty-state">Aucun volume : déclarez-en sous volumes: et montez-les dans un service pour garder ses données.</div>
  return (
    <div className="volume-cards">
      {stack.volumes.map((v) => (
        <VolumeCard key={v.name} stack={stack} volume={v} onChange={onChange} />
      ))}
    </div>
  )
}

const dateTime = (t: number) => new Date(t * 1000).toLocaleString('fr-FR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })

function VolumeCard({ stack, volume: v, onChange }: { stack: Stack; volume: StackVolume; onChange: () => void }) {
  const p = v.backup
  const [enabled, setEnabled] = useState(p?.enabled ?? false)
  const [schedule, setSchedule] = useState<BackupPolicy['schedule']>(p?.schedule ?? 'daily')
  const [hour, setHour] = useState(p?.hour ?? 3)
  const [keep, setKeep] = useState(p?.keep ?? 7)
  const [restoring, setRestoring] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const dirty = !p ? enabled : p.enabled !== enabled || p.schedule !== schedule || p.hour !== hour || p.keep !== keep

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
    <section className="panel volume-card">
      <header className="volume-card-head">
        <span className="header-title">
          <strong className="mono">{v.name}</strong>
          <span className="muted">
            {' '}
            · {v.sizeBytes >= 0 ? formatBytes(v.sizeBytes) : 'taille mesurée bientôt'} · {v.services.join(', ')}
          </span>
        </span>
        {v.postgres && <span className="badge">base postgres : pg_dump</span>}
        {v.restored && <span className="badge badge-accent">restauré</span>}
      </header>
      <small className="muted mono">{v.docker}</small>

      <div className="setting-row small-setting">
        <span className="header-title">
          <strong>Sauvegardes planifiées</strong>
          <span className="muted"> vers le stockage de l’instance</span>
        </span>
        <Switch checked={enabled} disabled={busy} onChange={setEnabled} label={`Sauvegarder ${v.name}`} />
      </div>
      {enabled && (
        <div className="form-row backup-form">
          <label className="field">
            <span>Quand</span>
            <select value={schedule} onChange={(e) => setSchedule(e.target.value as BackupPolicy['schedule'])}>
              <option value="daily">Tous les jours</option>
              <option value="hourly">Toutes les heures</option>
            </select>
          </label>
          {schedule === 'daily' && (
            <label className="field">
              <span>À</span>
              <select value={hour} onChange={(e) => setHour(Number(e.target.value))}>
                {Array.from({ length: 24 }, (_, h) => (
                  <option key={h} value={h}>
                    {String(h).padStart(2, '0')}:00
                  </option>
                ))}
              </select>
            </label>
          )}
          <label className="field">
            <span>Garder les</span>
            <input type="number" min={1} max={365} value={keep} onChange={(e) => setKeep(Number(e.target.value))} />
          </label>
        </div>
      )}
      {dirty && (
        <div>
          <button
            type="button"
            className="btn btn-primary btn-small"
            disabled={busy}
            onClick={() => run(() => (enabled || p ? api.saveBackupPolicy(stack.id, v.name, { schedule, hour, keep, enabled }) : Promise.resolve()))}
          >
            Enregistrer
          </button>
        </div>
      )}
      {p && (
        <p className="muted backup-status">
          {p.lastStatus === 'running' ? (
            <span className="state-warn">Sauvegarde en cours…</span>
          ) : p.lastRunAt ? (
            <>
              Dernière le {dateTime(p.lastRunAt)} :{' '}
              {p.lastStatus === 'ok' ? <span className="state-up">réussie</span> : <span className="state-down">échec · {p.lastError}</span>}
            </>
          ) : (
            'Pas encore de sauvegarde.'
          )}
          {p.enabled && p.nextRunAt && p.lastStatus !== 'running' ? ` Prochaine le ${dateTime(p.nextRunAt)}.` : ''}
        </p>
      )}
      {error && <p className="error">{error}</p>}
      {p && (
        <div className="volume-card-actions">
          <button type="button" className="btn btn-small" disabled={busy || p.lastStatus === 'running'} onClick={() => run(() => api.runBackup(stack.id, v.name))}>
            Sauvegarder maintenant
          </button>
          <button type="button" className="btn btn-small" disabled={busy} onClick={() => setRestoring(true)}>
            Restaurer…
          </button>
        </div>
      )}
      {p && p.restores.length > 0 && (
        <ul className="restore-list">
          {p.restores.map((r) => (
            <li key={r.id}>
              <span>
                <span className="mono">{r.snapshot.slice(0, 8)}</span> → <span className="mono">{r.volume}</span>
                <span className="muted"> · {dateTime(r.createdAt)}</span>
              </span>
              {r.status === 'running' && <span className="state-warn">restauration…</span>}
              {r.status === 'error' && <span className="state-down">échec : {r.error}</span>}
              {r.status === 'ok' &&
                (r.inUse ? (
                  <span className="badge badge-accent">utilisé</span>
                ) : (
                  <button
                    type="button"
                    className="btn btn-small"
                    disabled={busy}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Utiliser ${r.volume} à la place de ${v.name} ? ${v.services.join(', ')} redémarre${v.services.length > 1 ? 'nt' : ''} sur la sauvegarde ; le volume actuel reste sur le node.`,
                        )
                      )
                        run(() => api.useRestore(stack.id, v.name, r.id))
                    }}
                  >
                    Utiliser ce volume
                  </button>
                ))}
            </li>
          ))}
        </ul>
      )}
      {restoring && <RestoreModal stack={stack} volume={v} onClose={() => setRestoring(false)} onStarted={() => (setRestoring(false), onChange())} />}
    </section>
  )
}

function RestoreModal({ stack, volume, onClose, onStarted }: { stack: Stack; volume: StackVolume; onClose: () => void; onStarted: () => void }) {
  const [snapshots, setSnapshots] = useState<Snapshot[] | null>(null)
  const [picked, setPicked] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    api.snapshots(stack.id, volume.name).then(
      (s) => {
        setSnapshots(s)
        setPicked(s[0]?.id ?? '')
      },
      (err) => setError(errorMessage(err)),
    )
  }, [stack.id, volume.name])
  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api.restoreBackup(stack.id, volume.name, picked)
      onStarted()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }
  return (
    <Modal
      title={`Restaurer ${volume.name}`}
      subtitle="Dans un volume neuf du node : le volume actuel n’est pas touché."
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || !picked}>
            {busy ? 'Envoi…' : 'Restaurer'}
          </button>
        </>
      }
    >
      {!snapshots && !error && <p className="muted">Lecture des sauvegardes…</p>}
      {snapshots && snapshots.length === 0 && <p className="muted">Aucune sauvegarde de ce volume pour le moment.</p>}
      {snapshots && snapshots.length > 0 && (
        <div className="snapshot-list" role="radiogroup" aria-label="Sauvegarde">
          {snapshots.map((s) => (
            <label key={s.id} className={`snapshot ${picked === s.id ? 'on' : ''}`}>
              <input type="radio" name="snapshot" checked={picked === s.id} onChange={() => setPicked(s.id)} />
              <span>{dateTime(s.time)}</span>
              <span className="mono muted">{s.id.slice(0, 8)}</span>
              <span className="muted">{s.sizeBytes ? formatBytes(s.sizeBytes) : ''}</span>
            </label>
          ))}
        </div>
      )}
      {volume.postgres && (
        <p className="muted">
          Base postgres : un postgres de la même image démarre sur le volume neuf et y charge le dump avec pg_restore. Une fois prête, « Utiliser ce
          volume » fait redémarrer {volume.postgres} dessus.
        </p>
      )}
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

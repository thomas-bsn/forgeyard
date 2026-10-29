import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import {
  api,
  ApiError,
  errorMessage,
  type App,
  type BackupPolicy,
  type BindNode,
  type ComposeError,
  type ComposePortCandidate,
  type NodeChoice,
  type Snapshot,
  type Stack,
  type StackPreview,
  type StackState,
  type StackVolume,
} from './api'
import { appTone, Dot, LogoChooser, Logs, SourcePick, stateText, type Source } from './Apps'
import { AppLogo, formatBytes, Modal, OwnerChip, since, Switch, useRedeploy } from './ui'
import Access, { RoleBadge } from './Access'
import { addBindToCompose, composeServices } from './composeBind'

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

/** Adds a host folder of the node to a service of the Compose file, as the line it writes in `volumes:`. */
function BindHelper({ compose, setCompose, note }: { compose: string; setCompose: (c: string) => void; note?: string }) {
  const services = composeServices(compose)
  const [service, setService] = useState('')
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [readOnly, setReadOnly] = useState(true)
  const [message, setMessage] = useState('')
  const chosen = services.includes(service) ? service : (services[0] ?? '')
  const valid = chosen !== '' && source.trim().startsWith('/') && target.trim().startsWith('/')

  function add() {
    const next = addBindToCompose(compose, chosen, source.trim(), target.trim(), readOnly)
    if (next === null) {
      setMessage(`Je ne peux pas modifier ce fichier sans risque : ajoutez vous-même « - ${source.trim()}:${target.trim()}${readOnly ? ':ro' : ''} » sous volumes: du service ${chosen}.`)
      return
    }
    setCompose(next)
    setSource('')
    setTarget('')
    setMessage(`Ajouté au service ${chosen}.`)
  }

  return (
    <details className="field bind-helper">
      <summary>Monter un dossier du node</summary>
      <div className="env-list">
        <small>{note ?? 'Le dossier est ajouté au docker-compose.yml. Le projet est alors créé par une demande, que le superadmin approuve.'}</small>
        {services.length === 0 ? (
          <small className="muted">Écrivez d’abord un docker-compose.yml avec ses services.</small>
        ) : (
          <>
            <div className="bind-row">
              <label className="field">
                <span>Service</span>
                <select value={chosen} onChange={(e) => setService(e.target.value)}>
                  {services.map((s) => (
                    <option key={s} value={s}>
                      {s}
                    </option>
                  ))}
                </select>
              </label>
              <label className="field">
                <span>Dossier sur le node</span>
                <input className="mono" value={source} onChange={(e) => setSource(e.target.value)} placeholder="/srv/media" />
              </label>
              <label className="field">
                <span>Destination dans le conteneur</span>
                <input className="mono" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="/media" />
              </label>
              <label className="field">
                <span>Accès</span>
                <select value={readOnly ? 'ro' : 'rw'} onChange={(e) => setReadOnly(e.target.value === 'ro')}>
                  <option value="ro">Lecture seule</option>
                  <option value="rw">Lecture / écriture</option>
                </select>
              </label>
            </div>
            <div>
              <button type="button" className="btn btn-small" disabled={!valid} onClick={add}>
                Ajouter au Compose
              </button>
            </div>
          </>
        )}
        {message && <small role="status">{message}</small>}
      </div>
    </details>
  )
}

function ProjectBindChangeModal({ stack, onClose, onSubmitted }: { stack: Stack; onClose: () => void; onSubmitted: (id: number) => void }) {
  const [nodes, setNodes] = useState<BindNode[]>([])
  const [nodeId, setNodeId] = useState(stack.nodeId)
  const [compose, setCompose] = useState(stack.compose ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { api.bindNodes().then(setNodes, (err) => setError(errorMessage(err))) }, [])
  async function submit(e: FormEvent) {
    e.preventDefault(); setBusy(true); setError('')
    try {
      const request = await api.createBindChangeRequest({ change: { resourceId: stack.id, resourceType: 'stack', nodeId, stack: { name: stack.name, compose, registry: { server: stack.registry?.server ?? '', username: stack.registry?.username ?? '', password: '' } } } })
      onSubmitted(request.id)
    } catch (err) { setError(errorMessage(err)); setBusy(false) }
  }
  const revoked = stack.services.some((service) => service.bindMounts?.length && !service.bindAuthorized)
  return <Modal title={`Modifier les accès de ${stack.name}`} subtitle={revoked ? "L’autorisation est révoquée. Choisissez un autre node pour demander explicitement de nouveaux accès." : "Le Compose et les accès actifs restent en place jusqu’à la décision du superadmin."} onClose={onClose} onSubmit={submit}>
    <label className="field"><span>Node des chemins</span><select value={nodeId} onChange={(e)=>setNodeId(Number(e.target.value))}>{nodes.map((n)=><option key={n.id} value={n.id}>{n.name} · {n.online?'en ligne':'hors ligne'}</option>)}</select></label>
    <label className="field"><span>Compose avec les nouveaux bind mounts</span><textarea className="mono compose-editor" rows={18} value={compose} onChange={(e)=>setCompose(e.target.value)} required /></label>
    <BindHelper compose={compose} setCompose={setCompose} note="Le dossier est ajouté au Compose ci-dessus. Le superadmin approuve la demande avant qu’il soit monté." />
    <p className="muted">Conservez les volumes nommés. Les chemins hôtes ne sont pas copiés lors d’un changement de node.</p>
    {error && <p className="error" role="alert">{error}</p>}
    <button type="submit" className="btn btn-primary" disabled={busy || !nodeId}>{busy?'Envoi…':'Soumettre au superadmin'}</button>
  </Modal>
}

/** A project's picture: the one it was given, else the logos of its first services, overlapping. */
export function ProjectLogo({
  project,
  size,
}: {
  project: { name: string; logo: Stack['logo']; services: { id: number; name: string; logo: App['logo'] }[] }
  size: number
}) {
  if (project.logo.mode !== 'auto') return <AppLogo url={project.logo.url} color={project.logo.color} name={project.name} size={size} />
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

/** kept: a saved variable whose value the form never received; it stays as it is unless a value is typed. */
type EnvVar = { key: string; value: string; reveal?: boolean; kept?: boolean }

const secretName = /SECRET|PASSWORD|TOKEN|KEY|DATABASE_URL/i

/**
 * The variables as a list of names, complete, and the one picked below or beside it, with all the room for its
 * name and value. The value of a secret is hidden until it is edited or the eye is clicked.
 */
function EnvEditor({ env, setEnv, missing, requiredNames }: { env: EnvVar[]; setEnv: (e: EnvVar[]) => void; missing: { name: string; required: boolean }[]; requiredNames: string[] }) {
  const [sel, setSel] = useState(0)
  const [copied, setCopied] = useState(false)
  const requiredHintId = useId()
  const nameArea = useRef<HTMLTextAreaElement>(null)
  const valueArea = useRef<HTMLTextAreaElement>(null)
  const i = Math.min(sel, env.length - 1)
  const v = env[i] as EnvVar | undefined
  const secret = !!v && secretName.test(v.key) && v.value !== ''
  const requiredEmpty = (variable: EnvVar) => !variable.kept && requiredNames.includes(variable.key.trim()) && variable.value === ''
  const valueRequired = !!v && requiredNames.includes(v.key.trim())
  const valueMissing = !!v && requiredEmpty(v)

  useEffect(() => {
    for (const t of [nameArea.current, valueArea.current]) {
      if (!t) continue
      t.style.height = 'auto'
      t.style.height = `${t.scrollHeight + 2}px`
    }
  }, [i, v?.key, v?.value])

  const patch = (p: Partial<EnvVar>) => setEnv(env.map((x, j) => (j === i ? { ...x, ...p, ...(p.value !== undefined ? { kept: false } : {}) } : x)))
  const add = (key: string) => {
    setEnv([...env, { key, value: '' }])
    setSel(env.length)
  }
  const line = (e: string) => e.replace(/\n/g, '')

  return (
    <div className="env-list">
      {v && (
        <div className="env-split">
          <div className="env-names" role="listbox" aria-label="Variables">
            {env.map((x, j) => (
              <button key={j} type="button" role="option" aria-selected={j === i} aria-label={requiredEmpty(x) ? `${x.key}, obligatoire : valeur manquante` : undefined} className={`env-name ${j === i ? 'on' : ''} ${requiredEmpty(x) ? 'env-name-required' : ''}`} onClick={() => setSel(j)}>
                {x.key || <span className="muted">(sans nom)</span>}
              </button>
            ))}
          </div>
          <div className="env-detail">
            <label>
              <small className="muted">Nom</small>
              <textarea ref={nameArea} rows={1} aria-label="Nom de la variable" value={v.key} readOnly={v.kept} spellCheck={false} placeholder="NOM" onChange={(e) => patch({ key: line(e.target.value) })} onKeyDown={(e) => e.key === 'Enter' && e.preventDefault()} />
            </label>
            <label>
              <small className={valueMissing ? 'text-down' : 'muted'}>Valeur{valueRequired ? ' · obligatoire' : ''}</small>
              <textarea
                ref={valueArea}
                rows={2}
                aria-label="Valeur"
                aria-required={valueRequired || undefined}
                aria-invalid={valueMissing || undefined}
                aria-describedby={valueMissing ? requiredHintId : undefined}
                className={secret && !v.reveal ? 'env-masked' : ''}
                value={v.value}
                spellCheck={false}
                placeholder={v.kept ? 'inchangée' : 'valeur'}
                onFocus={() => v.reveal === undefined && secret && patch({ reveal: true })}
                onChange={(e) => patch({ value: line(e.target.value) })}
                onKeyDown={(e) => e.key === 'Enter' && e.preventDefault()}
              />
              {valueMissing && <small id={requiredHintId} className="text-down">Cette variable est obligatoire : renseignez une valeur.</small>}
            </label>
            <span className="env-tools">
              {v.value !== '' && (
                <button
                  type="button"
                  className="btn btn-small"
                  onClick={() => {
                    navigator.clipboard?.writeText(v.value)
                    setCopied(true)
                    setTimeout(() => setCopied(false), 1500)
                  }}
                >
                  {copied ? 'Copié' : 'Copier la valeur'}
                </button>
              )}
              {secret && (
                <button type="button" className="btn btn-small" onClick={() => patch({ reveal: !v.reveal })}>
                  {v.reveal ? 'Masquer' : 'Afficher'}
                </button>
              )}
              <button
                type="button"
                className="btn btn-small"
                onClick={() => {
                  setEnv(env.filter((_, j) => j !== i))
                  setSel(Math.max(0, i - 1))
                }}
              >
                Retirer
              </button>
            </span>
          </div>
        </div>
      )}
      {missing.length > 0 && (
        <span className="chips-row">
          {missing.map((m) => (
            <button
              key={m.name}
              type="button"
              className={`chip mono ${m.required ? 'chip-required' : ''}`}
              title={m.required ? 'Obligatoire : le compose n’a pas de valeur par défaut' : 'Facultatif : le compose a une valeur par défaut'}
              onClick={() => add(m.name)}
            >
              + {m.name}
            </button>
          ))}
        </span>
      )}
      {(missing.some((m) => m.required) || env.some(requiredEmpty)) && <small className="text-down">En rouge : variables obligatoires encore à renseigner, sans valeur par défaut dans le Compose.</small>}
      <div>
        <button type="button" className="btn btn-dashed btn-small" onClick={() => add('')}>
          + Ajouter une variable
        </button>
      </div>
    </div>
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

export const kindLabels: Record<string, string> = { web: 'exposé', service: 'privé', job: 'job', sandbox: 'sandbox' }

export function ProjectFormModal({
  stack,
  admin,
  superadmin,
  onClose,
  onSaved,
  onSource,
  onRequest,
}: {
  stack?: Stack
  admin?: boolean
  superadmin?: boolean
  onClose: () => void
  onSaved: (s: Stack) => void
  // onSource: another source picked in Nouvelle app, which then shows its own form.
  onSource?: (s: Source) => void
  onRequest?: (id: number) => void
}) {
  const [name, setName] = useState(stack?.name ?? '')
  const [compose, setCompose] = useState(stack?.compose ?? '')
  const [env, setEnv] = useState<EnvVar[]>((stack?.envKeys ?? []).map((key) => ({ key, value: '', kept: true })))
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
  const [bindNodes, setBindNodes] = useState<BindNode[]>([])
  // The folders of the node the Compose mounts: found in the file itself, they make the creation a request.
  const binds = (!stack && preview?.binds) || []
  const useBindMounts = binds.length > 0
  const [domain, setDomain] = useState('')
  const [flowStep, setFlowStep] = useState(0)
  const [adaptationNeeded, setAdaptationNeeded] = useState(false)
  const [portCandidates, setPortCandidates] = useState<ComposePortCandidate[]>([])
  const [exposeChoice, setExposeChoice] = useState('')
  const [adaptedCompose, setAdaptedCompose] = useState('')
  const [adaptedPreview, setAdaptedPreview] = useState<StackPreview | null>(null)
  const [adaptedErrors, setAdaptedErrors] = useState<ComposeError[]>([])
  const [adaptChecking, setAdaptChecking] = useState(false)
  const [adaptError, setAdaptError] = useState('')
  const [adaptBusy, setAdaptBusy] = useState(false)
  const editor = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    api.instance().then((i) => setDomain(i.appsDomain ?? ''), () => {})
    if (stack || !admin) return
    api.nodeChoices().then((c) => {
      setChoices(c)
      setNodeId(c.find((x) => x.recommended)?.id ?? 0)
    }, () => {})
  }, [admin, stack])

  useEffect(() => {
    if (!useBindMounts || stack) return
    api.bindNodes().then((nodes) => {
      setBindNodes(nodes)
      setNodeId((current) => (nodes.some((n) => n.id === current) ? current : nodes.find((n) => n.online)?.id || nodes[0]?.id || 0))
    }, (err) => setError(errorMessage(err)))
  }, [useBindMounts, stack])

  const vars = Object.fromEntries(env.filter((v) => v.key.trim() && !v.kept).map((v) => [v.key.trim(), v.value]))
  const keep = env.filter((v) => v.key.trim() && v.kept).map((v) => v.key.trim())
  const varsKey = JSON.stringify([vars, keep])
  // Checked as it is typed, once typing pauses.
  useEffect(() => {
    if (!compose.trim()) {
      setPreview(null)
      setErrors([])
      return
    }
    const t = setTimeout(() => {
      api.validateStack({ name: stack?.name ?? (name.trim() || 'projet'), compose, env: vars, keepEnv: keep, stackId: stack?.id }).then(
        (p) => {
          setPreview(p)
          setErrors(p.errors)
        },
        () => {},
      )
    }, 400)
    return () => clearTimeout(t)
  }, [compose, varsKey, name])

  useEffect(() => {
    if (!adaptedCompose) return
    setAdaptChecking(true)
    const t = setTimeout(() => {
      api.validateStack({ name: name.trim() || 'projet', compose: adaptedCompose, env: vars }).then((p) => {
        setAdaptedPreview(p)
        setAdaptedErrors(p.errors)
        setAdaptChecking(false)
      }, () => setAdaptChecking(false))
    }, 300)
    return () => clearTimeout(t)
  }, [adaptedCompose, varsKey, name])

  const variables = usedVariables(compose)
  const missing = variables.filter((v) => !(v.name in vars) && !keep.includes(v.name))
  const requiredNames = variables.filter((v) => v.required).map((v) => v.name)
  const requiredMissing = requiredNames.filter((name) => !vars[name] && !keep.includes(name))

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
    if (!stack && flowStep < 3) {
      if (flowStep === 2) setFlowStep(3)
      return
    }
    setBusy(true)
    setError('')
    try {
      const login = registryTouched || (!stack && registry.server.trim()) ? registry : undefined
      if (!stack && useBindMounts) {
        const request = await api.createBindRequest({ nodeId, bindMounts: [], stack: { name, compose, env: vars, registry: login } })
        if (request.status === 'created' && request.stackId) onSaved(await api.stack(request.stackId))
        else onRequest?.(request.id)
        return
      }
      const saved = stack ? await api.updateStack(stack.id, { compose, env: vars, keepEnv: keep, registry: login }) : await api.createStack({ name, compose, env: vars, nodeId: nodeId || undefined, registry: login })
      onSaved(saved)
    } catch (err) {
      if (err instanceof ApiError && err.composeErrors) setErrors(err.composeErrors)
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  async function analyzeCompose() {
    setAdaptBusy(true)
    setAdaptError('')
    try {
      const result = await api.adaptStackPorts({ compose })
      if (result.error) throw new Error(result.error)
      setPortCandidates(result.candidates ?? [])
      setExposeChoice('')
      setAdaptedCompose('')
      setAdaptedPreview(null)
      setAdaptedErrors([])
      // Compose files without published ports are already compatible: skip the
      // adaptation review and continue straight to resource configuration.
      const adaptationNeeded = (result.candidates ?? []).length > 0
      setFlowStep(adaptationNeeded ? 1 : 2)
      setAdaptationNeeded(adaptationNeeded)
    } catch (err) {
      setAdaptError(errorMessage(err))
    } finally {
      setAdaptBusy(false)
    }
  }

  async function prepareAdaptation() {
    setAdaptBusy(true)
    setAdaptError('')
    try {
      const [service = '', rawPort = ''] = exposeChoice.split(':')
      const result = await api.adaptStackPorts({ compose, apply: true, exposeService: service, exposePort: Number(rawPort) || 0 })
      if (result.error || !result.compose) throw new Error(result.error || 'Impossible de produire le Compose adapté')
      setAdaptedCompose(result.compose)
      setAdaptChecking(true)
      const checked = await api.validateStack({ name: name.trim() || 'projet', compose: result.compose, env: vars })
      setAdaptedErrors(checked.errors)
      setAdaptedPreview(checked)
      setAdaptChecking(false)
    } catch (err) {
      setAdaptError(errorMessage(err))
      setAdaptChecking(false)
    } finally {
      setAdaptBusy(false)
    }
  }

  function acceptAdaptation() {
    setCompose(adaptedCompose)
    setFlowStep(2)
    setError('')
  }

  const shownName = stack?.name ?? (name.trim() || 'mon-projet')
  const serviceList = (data: StackPreview | null) => data?.services.length ? (
    <ol className="stack-preview">
      {data.services.map((s) => (
        <li key={s.name}>
          <span className="stack-preview-head">
            <strong>{s.name}</strong>
            <span className={`badge ${s.kind === 'web' ? 'badge-accent' : ''}`}>{s.kind === 'web' ? `exposé :${s.port}` : kindLabels[s.kind]}</span>
            {s.backup && <span className="badge">pg_dump</span>}
          </span>
          <span className="mono muted">{s.image}</span>
          <span className="muted">Mémoire : {s.memoryMb ?? 512} Mo</span>
          {s.kind === 'web' && domain && <span className="muted">→ {shownName}.{domain}</span>}
          {s.dependsOn.length > 0 && <span className="muted">après {s.dependsOn.map((d) => d.service).join(', ')}</span>}
        </li>
      ))}
    </ol>
  ) : <p className="muted aside-note">Les services apparaîtront ici après validation.</p>

  const legacyAside = (
    <>
      <span className="aside-label">Aperçu</span>
      {preview?.error && <p className="error">{preview.error}</p>}
      {serviceList(preview)}
      {!!preview?.volumes?.length && <dl className="aside-facts"><dt>Volumes</dt><dd className="mono">{preview.volumes.join(', ')}</dd></dl>}
      {stack && <p className="muted aside-note">Seuls les services modifiés sont redéployés.</p>}
      <span className="aside-spacer" />
      {error && <p className="error">{error}</p>}
      <button type="submit" className="btn btn-primary btn-block btn-big" disabled={busy || errors.length > 0}>
        {busy ? 'Envoi…' : 'Enregistrer et déployer'}
      </button>
      <button type="button" className="link-button aside-cancel" onClick={onClose} disabled={busy}>Annuler</button>
    </>
  )

  // The final submit button has its own key: reusing the clicked review button would change its
  // type to submit before the browser handles that same click's default action.
  const wizardAside = (
    <>
      <span className="aside-label">Création du projet</span>
      <ol className="compose-steps">
        {(adaptationNeeded ? ['Importer et analyser', 'Relire l’adaptation', 'Configurer', 'Confirmer'] : ['Importer et analyser', 'Configurer', 'Confirmer']).map((label, index) => {
          const step = !adaptationNeeded && index > 0 ? index + 1 : index
          return <li key={label} className={flowStep === step ? 'current' : flowStep > step ? 'done' : ''}>
            <span>{flowStep > step ? '✓' : index + 1}</span>{label}
          </li>
        })}
      </ol>
      {flowStep >= 2 && <div className="compose-aside-resources">
        <span className="aside-label">Services et ressources</span>
        {serviceList(preview)}
        {!!preview?.volumes?.length && <p className="muted">Volumes : <span className="mono">{preview.volumes.join(', ')}</span></p>}
      </div>}
      <div className="aside-spacer" />
      {adaptError && <p className="error">{adaptError}</p>}
      {error && <p className="error">{error}</p>}
      {flowStep === 0 ? (
        <button type="button" className="btn btn-primary btn-block btn-big" disabled={adaptBusy || !compose.trim()} onClick={analyzeCompose}>
          {adaptBusy ? 'Analyse…' : 'Analyser le Compose'}
        </button>
      ) : flowStep === 1 ? (
        <button type="button" className="btn btn-primary btn-block btn-big" disabled={adaptBusy || !!adaptedCompose} onClick={prepareAdaptation}>
          {adaptBusy ? 'Préparation…' : adaptedCompose ? 'Adaptation prête à relire' : 'Préparer l’adaptation'}
        </button>
      ) : flowStep === 2 ? (
        <button type="button" className="btn btn-primary btn-block btn-big" disabled={errors.length > 0 || !!preview?.error} onClick={() => setFlowStep(3)}>Vérifier le récapitulatif</button>
      ) : (
        <button key="confirm-compose" type="submit" className="btn btn-primary btn-block btn-big" disabled={busy || errors.length > 0 || !!preview?.error || (useBindMounts && !nodeId)}>
          {busy ? 'Envoi…' : useBindMounts && !superadmin ? 'Envoyer la demande' : 'Confirmer et créer'}
        </button>
      )}
      {flowStep > 0 && flowStep < 3 && <button type="button" className="link-button aside-cancel" onClick={() => { setFlowStep(flowStep === 2 && !adaptationNeeded ? 0 : flowStep - 1); setAdaptedCompose(''); setAdaptError('') }}>Retour</button>}
      <button type="button" className="link-button aside-cancel" onClick={onClose} disabled={busy}>Annuler</button>
    </>
  )

  const wizard = (
    <>
      {flowStep === 0 && <>
        {onSource && <SourcePick source="compose" onPick={onSource} />}
        <label className="field">
          <span>Nom du projet</span>
          <input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="shameless" required autoFocus pattern="[a-z0-9]([a-z0-9\-]{0,30}[a-z0-9])?" />
          <small>Le service exposé prend ce nom de sous-domaine.</small>
        </label>
        <label className="field">
          <span>docker-compose.yml</span>
          <textarea ref={editor} className="mono compose-editor" value={compose} onChange={(e) => setCompose(e.target.value)} spellCheck={false} rows={16} required placeholder={composeExample} aria-invalid={errors.length > 0} />
          <small>Les règles locales analysent les ports et les ressources. Le fichier n’est pas enregistré avant ta confirmation.</small>
        </label>
        <BindHelper compose={compose} setCompose={setCompose} />
        {errors.length > 0 && <ul className="compose-errors" aria-live="polite">{errors.map((e, i) => <li key={i}>{e.line > 0 && <button type="button" className="link-button" onClick={() => goToLine(e.line)}>ligne {e.line}</button>}<span>{e.message}</span></li>)}</ul>}
      </>}
      {flowStep === 1 && <>
        <div className="compose-step-heading"><span className="aside-label">Étape 2 sur 4</span><h3>Relire l’adaptation proposée</h3><p>Les ports déclarés deviennent privés au projet. Choisis au plus un service à publier en HTTPS.</p></div>
        <div className="field">
          <span className="label">Service web à publier <span className="muted">· optionnel</span></span>
          <select value={exposeChoice} onChange={(e) => { setExposeChoice(e.target.value); setAdaptedCompose(''); setAdaptError('') }}>
            <option value="">Aucun service exposé</option>
            {portCandidates.filter((c) => c.protocol === 'tcp').map((c, i) => <option key={`${c.service}-${c.target}-${i}`} value={`${c.service}:${c.target}`}>{c.service} · port conteneur {c.target}{c.published ? ` (port hôte ${c.published})` : ''}</option>)}
          </select>
          <small>Les autres services restent joignables entre eux par leur nom, sans port public.</small>
        </div>
        <div className="compose-change-list">
          <div><b>{portCandidates.length} port{portCandidates.length === 1 ? '' : 's'} détecté{portCandidates.length === 1 ? '' : 's'}</b><p>{portCandidates.length ? portCandidates.map((c) => `${c.service} ${c.published ? `${c.published}:` : ''}${c.target}/${c.protocol}`).join(' · ') : 'Aucun port Compose à convertir.'}</p></div>
          <div><b>Ce qui change</b><p>Les publications machine sont retirées. Le service choisi reçoit <code>x-forgeyard.expose</code> et une adresse HTTPS.</p></div>
        </div>
        {portCandidates.some((c) => c.protocol !== 'tcp') && <p className="compose-adapt-warning">Les ports UDP ne peuvent pas être exposés par Forgeyard. Ils seront retirés du Compose adapté ; vérifie que ces services n’en dépendent pas.</p>}
        {adaptedCompose && <>
          <label className="field"><span className="label">Compose adapté · aperçu modifiable</span><textarea className="mono compose-editor" value={adaptedCompose} onChange={(e) => { setAdaptedErrors([]); setAdaptChecking(true); setAdaptedCompose(e.target.value) }} rows={13} spellCheck={false} /></label>
          {adaptedErrors.length > 0 && <ul className="compose-errors">{adaptedErrors.map((e, i) => <li key={i}>{e.line > 0 && <span>ligne {e.line} · </span>}{e.message}</li>)}</ul>}
          {adaptChecking && <p className="muted">Validation du Compose adapté…</p>}
          {adaptedErrors.length === 0 && <div className="compose-adapted-preview"><span className="aside-label">Services et ressources · un seul récapitulatif</span>{serviceList(adaptedPreview)}{adaptedPreview?.volumes.length ? <p className="muted">Volumes : <span className="mono">{adaptedPreview.volumes.join(', ')}</span></p> : null}</div>}
        </>}
      </>}
      {flowStep === 2 && <>
        <div className="compose-step-heading"><span className="aside-label">Étape {adaptationNeeded ? '3 sur 4' : '2 sur 3'}</span><h3>Configurer le projet</h3><p>Les services et volumes apparaissent une seule fois dans le récapitulatif à droite.</p></div>
        <label className="field"><span className="label">Nom du projet</span><input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} required pattern="[a-z0-9]([a-z0-9\-]{0,30}[a-z0-9])?" /></label>
        <div className="field"><span className="label">Variables chiffrées</span><EnvEditor env={env} setEnv={setEnv} missing={missing} requiredNames={requiredNames} /></div>
        {!useBindMounts && <div className="field"><span className="label">Node</span><select value={nodeId} onChange={(e) => setNodeId(Number(e.target.value))}><option value={0}>Choix recommandé automatiquement</option>{choices.map((c) => <option key={c.id} value={c.id}>{c.name}{c.recommended ? ' (recommandé)' : ''}</option>)}</select></div>}
        {useBindMounts && <section className="bind-card" aria-label="Dossiers du node">
          <div className="bind-card-head"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" /></svg><div><strong>Dossiers du node</strong><span>{binds.length === 1 ? '1 dossier trouvé' : `${binds.length} dossiers trouvés`} dans le Compose</span></div></div>
          {binds.map((b, i) => <div className="bind-row" key={i}><span><code>{b.source}</code><small>service {b.service}</small></span><code>{b.target}</code><span className="bind-mode">{b.readOnly ? 'Lecture seule' : 'Lecture et écriture'}</span></div>)}
          <label className="bind-node"><span>Node qui héberge ces dossiers</span><select value={nodeId} onChange={(e) => setNodeId(Number(e.target.value))} required>{bindNodes.map((n) => <option key={n.id} value={n.id}>{n.name} · {n.online ? 'en ligne' : 'hors ligne'}</option>)}</select></label>
        </section>}
        {useBindMounts && <p className={`bind-note ${superadmin ? 'ok' : ''}`}>{superadmin ? 'Tu es superadmin : ces dossiers sont autorisés automatiquement, sans demande.' : 'Monter un dossier du node demande l’accord d’un superadmin. Le projet est créé quand il approuve, et tu suis la demande dans Support.'}</p>}
        <details className="field" open={registryOpen} onToggle={(e) => setRegistryOpen(e.currentTarget.open)}><summary>Registry privé <span className="muted">{registry.server ? `· ${registry.server}` : '· facultatif'}</span></summary><div className="env-list"><input aria-label="Adresse du registry" className="mono" value={registry.server} onChange={(e) => editRegistry({ server: e.target.value })} placeholder="ghcr.io" /><div className="env-row"><input aria-label="Identifiant" value={registry.username} onChange={(e) => editRegistry({ username: e.target.value })} placeholder="identifiant" autoComplete="off" /><input type="password" aria-label="Mot de passe ou token" value={registry.password} onChange={(e) => editRegistry({ password: e.target.value })} placeholder="token" autoComplete="new-password" /></div><small>Le token est chiffré avant l’enregistrement.</small></div></details>
        {errors.length > 0 && <ul className="compose-errors">{errors.map((e, i) => <li key={i}>{e.message}</li>)}</ul>}
      </>}
      {flowStep === 3 && <>
        <div className="compose-step-heading"><span className="aside-label">Étape {adaptationNeeded ? '4 sur 4' : '3 sur 3'}</span><h3>Confirmer le projet</h3><p>Vérifie les ressources avant la création.</p></div>
        <dl className="compose-confirm-facts"><dt>Projet</dt><dd>{shownName}</dd><dt>Node</dt><dd>{(useBindMounts ? bindNodes : choices).find((c) => c.id === nodeId)?.name ?? 'Choix recommandé automatiquement'}</dd><dt>Variables requises</dt><dd>{requiredMissing.length ? `${requiredMissing.length} à compléter` : 'Complétées'}</dd>{useBindMounts ? <><dt>Dossiers du node</dt><dd>{binds.map((b, i) => <div key={i}><code>{b.source}</code> → <code>{b.target}</code> · {b.readOnly ? 'lecture seule' : 'lecture et écriture'}</div>)}</dd><dt>Autorisation</dt><dd>{superadmin ? 'Automatique (superadmin)' : 'À approuver par un superadmin'}</dd></> : null}<dt>Registry privé</dt><dd>{registry.server || 'Aucun'}</dd></dl>
        <details className="field"><summary>Relire le Compose final</summary><pre className="mono compose-final">{compose}</pre></details>
      </>}
    </>
  )

  if (stack) return <Modal title={`Projet ${stack.name}`} onClose={onClose} onSubmit={submit} aside={legacyAside}>
    <label className="field"><span>docker-compose.yml</span><textarea ref={editor} className="mono compose-editor" value={compose} onChange={(e) => setCompose(e.target.value)} spellCheck={false} rows={20} required placeholder={composeExample} aria-invalid={errors.length > 0} /></label>
    <small className="muted">Les dossiers du node ne se modifient pas ici : « Monter un dossier du node… » sur la page du projet envoie la demande au superadmin.</small>
    {errors.length > 0 && <ul className="compose-errors" aria-live="polite">{errors.map((e, i) => <li key={i}>{e.line > 0 && <button type="button" className="link-button" onClick={() => goToLine(e.line)}>ligne {e.line}</button>}<span>{e.message}</span></li>)}</ul>}
    <div className="field"><span>Variables <span className="muted">· chiffrées</span></span><EnvEditor env={env} setEnv={setEnv} missing={missing} requiredNames={requiredNames} />{!stack.role && env.some((v) => v.kept) && <div><button type="button" className="btn btn-small" onClick={async () => { try { const { env: values } = await api.stackEnv(stack.id); setEnv(env.map((v) => (v.kept ? { key: v.key, value: values[v.key] ?? '' } : v))) } catch (err) { setError(errorMessage(err)) } }}>Afficher les valeurs</button></div>}<small className="muted">Une valeur laissée vide sur une variable déjà enregistrée reste inchangée.</small></div>
    <details className="field" open={registryOpen} onToggle={(e) => setRegistryOpen(e.currentTarget.open)}><summary>Registry privé <span className="muted">{registry.server ? `· ${registry.server}` : '· images publiques'}</span></summary><div className="env-list"><input aria-label="Adresse du registry" className="mono" value={registry.server} onChange={(e) => editRegistry({ server: e.target.value })} placeholder="ghcr.io" /><div className="env-row"><input aria-label="Identifiant" value={registry.username} onChange={(e) => editRegistry({ username: e.target.value })} placeholder="identifiant" autoComplete="off" /><input type="password" aria-label="Mot de passe ou token" value={registry.password} onChange={(e) => editRegistry({ password: e.target.value })} placeholder={stack.registry?.passwordSet ? 'inchangé' : 'token'} autoComplete="new-password" /></div></div></details>
  </Modal>

  return <Modal title="Nouvelle app · Docker Compose" subtitle="Étapes guidées · aucune création avant confirmation" onClose={onClose} onSubmit={submit} aside={wizardAside}>
    <ol className="compose-stepper">{(adaptationNeeded ? ['Source', 'Adaptation', 'Ressources', 'Confirmation'] : ['Source', 'Ressources', 'Confirmation']).map((label, index) => { const step = !adaptationNeeded && index > 0 ? index + 1 : index; return <li key={label} className={flowStep === step ? 'current' : flowStep > step ? 'done' : ''}><span>{flowStep > step ? '✓' : index + 1}</span>{label}</li> })}</ol>
    {wizard}
    {flowStep === 1 && adaptedCompose && adaptedErrors.length === 0 && !adaptChecking && <div className="compose-review-actions"><button type="button" className="link-button" onClick={() => { setAdaptedCompose(''); setFlowStep(0) }}>Revenir au Compose source</button><button type="button" className="btn btn-primary" onClick={acceptAdaptation}>Accepter cette adaptation</button></div>}
    {flowStep > 1 && <div className="compose-review-actions"><button type="button" className="link-button" onClick={() => setFlowStep(flowStep === 2 && !adaptationNeeded ? 0 : flowStep - 1)}>← Étape précédente</button>{flowStep === 2 && <button type="button" className="btn btn-primary" disabled={errors.length > 0 || !!preview?.error} onClick={() => setFlowStep(3)}>Continuer vers la confirmation →</button>}</div>}
  </Modal>
}

type StackTab = 'services' | 'logs' | 'volumes' | 'compose' | 'access'

export function ProjectDetail({
  stack,
  onChange,
  go,
  readOnly,
  superadmin,
}: {
  stack: Stack
  onChange: () => void
  go: (path: string) => void
  readOnly?: boolean // another member's project, opened from their profile
  superadmin?: boolean
}) {
  const [full, setFull] = useState<Stack | null>(null)
  const [tab, setTab] = useState<StackTab>('services')
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [logsOf, setLogsOf] = useState(stack.services.find((s) => s.kind === 'web')?.id ?? stack.services[0]?.id)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [changingBinds, setChangingBinds] = useState(false)

  useEffect(() => {
    // The compose file and variables come with the project's own page only.
    if (readOnly) return
    api.stack(stack.id).then(setFull).catch((err) => setError(errorMessage(err)))
  }, [stack.id, stack.updatedAt, readOnly])

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
  const [choosingLogo, setChoosingLogo] = useState(false)
  const failedJob = stack.services.find((s) => s.state === 'failed')
  // No role: the owner or an admin. A viewer only reads; a contributor runs and edits, without the owner's rights.
  const isOwner = !stack.role
  const canWrite = stack.role !== 'viewer'

  return (
    <div className="detail">
      <aside className="panel detail-side">
        <a href={readOnly ? `#/u/${stack.ownerId}` : '#/apps'} className="back-link">
          ← {readOnly ? stack.ownerName : 'Apps'}
        </a>
        <div className="detail-title">
          {readOnly || !canWrite ? (
            <ProjectLogo project={stack} size={52} />
          ) : (
            <button type="button" className="logo-button" onClick={() => setChoosingLogo(true)} aria-label="Changer le logo" title="Changer le logo">
              <ProjectLogo project={stack} size={52} />
              <span className="logo-edit" aria-hidden="true">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
                </svg>
              </span>
            </button>
          )}
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
          <p className="hint">
            {readOnly ? 'Aucun service exposé.' : <>Aucun service exposé : ajoutez x-forgeyard: {'{ expose: { port: 80 } }'} au service à publier.</>}
          </p>
        )}
        {failedJob && (
          <div className="banner banner-down">
            <span className="dot dot-down" />
            <span>
              Le job <strong>{failedJob.service}</strong> a échoué : les services qui l’attendent ne démarrent pas.
              {readOnly ? null : (
                <>
                  {' '}
                  Voir{' '}
                  <button type="button" className="link-button" onClick={() => (setLogsOf(failedJob.id), setTab('logs'))}>
                    ses logs
                  </button>
                  , corrigez, puis redéployez.
                </>
              )}
            </span>
          </div>
        )}
        <dl className="kv">
          {!readOnly && (
            <>
              <dt>Node</dt>
              <dd>{stack.nodeName}</dd>
              <dt>Réseau</dt>
              <dd className="mono">forgeyard-stack-{stack.id}</dd>
            </>
          )}
          <dt>Propriétaire</dt>
          <dd>
            <OwnerChip id={stack.ownerId} name={stack.ownerName} />
          </dd>
          <dt>Modifiée</dt>
          <dd>{since(stack.updatedAt)}</dd>
        </dl>
        {error && <p className="error">{error}</p>}
        {!readOnly && stack.role && (
          <p className="muted">
            <RoleBadge role={stack.role} /> {canWrite ? 'Vous pouvez le déployer et le configurer.' : 'Vous le consultez en lecture seule.'}
          </p>
        )}
        {!readOnly && canWrite && (
          <>
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
              {isOwner && <button type="button" className="btn detail-actions-wide" disabled={busy || !full} onClick={() => setChangingBinds(true)}>{!stack.services.some((service) => service.bindMounts?.length) ? 'Monter un dossier du node…' : stack.services.some((service) => service.bindMounts?.length && !service.bindAuthorized) ? 'Demander de nouveaux accès sur un autre node' : 'Demander une modification des accès / du node'}</button>}
              {superadmin && stack.services.some((service) => service.bindMounts?.length && service.bindAuthorized) && <button type="button" className="btn danger detail-actions-wide" disabled={busy} onClick={() => run(() => api.revokeStackBinds(stack.id))}>Révoquer les bind mounts du projet</button>}
            </div>
            {changingBinds && full && <ProjectBindChangeModal stack={full} onClose={() => setChangingBinds(false)} onSubmitted={(id) => { setChangingBinds(false); go(`support/bind/${id}`) }} />}
            {isOwner && (
              <button type="button" className="link-button danger-link" disabled={busy} onClick={() => setDeleting(true)}>
                Supprimer le projet…
              </button>
            )}
          </>
        )}
      </aside>

      <main className="detail-main">
        {readOnly ? (
          <ServiceList services={stack.services} readOnly />
        ) : (
          <>
            <div className="segmented" role="tablist" aria-label="Vues du projet">
              {(
                [
                  ['services', 'Apps du projet'],
                  ['logs', 'Logs'],
                  ['volumes', 'Volumes et sauvegardes'],
                  ...(canWrite ? ([['compose', 'Compose']] as const) : []),
                  ['access', 'Accès'],
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
                {full.envKeys && full.envKeys.length > 0 && (
                  <p className="muted">
                    Variables : <span className="mono">{full.envKeys.join(', ')}</span>
                  </p>
                )}
              </div>
            )}
            {tab === 'access' && <Access kind="stacks" id={stack.id} name={stack.name} owner={isOwner} onTransferred={() => go('apps')} />}
          </>
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
      {choosingLogo && (
        <LogoChooser
          name={stack.name}
          logo={stack.logo}
          autoPreview={<ProjectLogo project={{ ...stack, logo: { mode: 'auto' } }} size={48} />}
          autoText="Les logos des premiers services du projet."
          save={(body) => api.setStackLogo(stack.id, body)}
          onClose={() => setChoosingLogo(false)}
          onSaved={() => {
            setChoosingLogo(false)
            onChange()
          }}
        />
      )}
      {deleting && <DeleteStack stack={stack} onClose={() => setDeleting(false)} onDeleted={() => (onChange(), go('apps'))} />}
    </div>
  )
}

function ServiceList({ services, readOnly }: { services: App[]; readOnly?: boolean }) {
  return (
    <ol className="panel stack-services">
      {services.map((s) => (
        <li key={s.id}>
          <Dot tone={appTone(s)} />
          <span className="stack-service-main">
            <span className="stack-preview-head">
              {readOnly ? (
                <strong>{s.service}</strong>
              ) : (
                <a href={`#/apps/${s.id}`}>
                  <strong>{s.service}</strong>
                </a>
              )}
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

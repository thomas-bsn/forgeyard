import { useEffect, useState, type FormEvent } from 'react'
import { api, errorMessage, type JoinCommand, type Node } from './api'
import { CopyField } from './ui'

const POLL_MS = 5000

export function formatBytes(n: number): string {
  if (n >= 1e12) return `${(n / 1e12).toFixed(1)} To`
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} Go`
  if (n >= 1e6) return `${Math.round(n / 1e6)} Mo`
  return `${Math.round(n / 1e3)} Ko`
}

function since(unix?: number): string {
  if (!unix) return 'jamais'
  const min = Math.round((Date.now() / 1000 - unix) / 60)
  if (min < 1) return "à l'instant"
  if (min < 60) return `il y a ${min} min`
  if (min < 48 * 60) return `il y a ${Math.round(min / 60)} h`
  return new Date(unix * 1000).toLocaleDateString('fr-FR')
}

export default function Nodes({ localSupported }: { localSupported: boolean }) {
  const [nodes, setNodes] = useState<Node[] | null>(null)
  const [error, setError] = useState('')
  const [join, setJoin] = useState<JoinCommand | null>(null)

  async function load() {
    try {
      setNodes(await api.nodes())
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  useEffect(() => {
    load()
    const id = setInterval(load, POLL_MS)
    return () => clearInterval(id)
  }, [])

  return (
    <div className="section">
      {nodes && (
        <AddNode
          localAvailable={localSupported && !nodes.some((n) => n.isLocal)}
          onCreated={(j) => {
            setJoin(j)
            load()
          }}
        />
      )}
      {join && <JoinInstructions join={join} onClose={() => setJoin(null)} />}
      {error && <p className="error">{error}</p>}
      {nodes && nodes.length === 0 && (
        <div className="empty-state">
          <strong>Aucun node</strong>
          <span>Un node est une machine qui fait tourner les conteneurs. Ajoutez-en un pour commencer.</span>
        </div>
      )}
      {nodes && nodes.length > 0 && (
        <div className="node-grid">
          {nodes.map((n) => (
            <NodeCard key={n.id} node={n} onJoin={setJoin} onChange={load} />
          ))}
        </div>
      )}
    </div>
  )
}

function AddNode({ localAvailable, onCreated }: { localAvailable: boolean; onCreated: (j: JoinCommand) => void }) {
  const [where, setWhere] = useState<'local' | 'remote'>(localAvailable ? 'local' : 'remote')
  const local = localAvailable && where === 'local'
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      onCreated(await api.createNode(local ? 'local' : name, local))
      setName('')
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel" onSubmit={submit}>
      <h2>Ajouter un node</h2>
      {localAvailable && (
        <div className="auth-options">
          <button type="button" className={`auth-option ${where === 'local' ? 'selected' : ''}`} onClick={() => setWhere('local')} aria-pressed={where === 'local'}>
            <strong>Cette machine</strong>
            <small>Celle où tourne Forgeyard. Rien à installer.</small>
          </button>
          <button type="button" className={`auth-option ${where === 'remote' ? 'selected' : ''}`} onClick={() => setWhere('remote')} aria-pressed={where === 'remote'}>
            <strong>Une autre machine</strong>
            <small>Un autre serveur avec Docker.</small>
          </button>
        </div>
      )}
      <div className="add-node">
        {!local && (
          <label className="field">
            <span>Nom du node</span>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="serveur-b"
              pattern="[a-zA-Z0-9][a-zA-Z0-9\-]{0,31}"
              title="1 à 32 caractères : lettres, chiffres et -"
              required
            />
          </label>
        )}
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? 'Création…' : local ? 'Activer cette machine' : 'Créer'}
        </button>
      </div>
      {error && <p className="error">{error}</p>}
    </form>
  )
}

function JoinInstructions({ join, onClose }: { join: JoinCommand; onClose: () => void }) {
  const expires = new Date(join.expiresAt * 1000).toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' })
  return (
    <div className="panel join-panel">
      <div className="header">
        <h2 className="header-title">{join.node.isLocal ? 'Cette machine' : `Connecter « ${join.node.name} »`}</h2>
        <button type="button" className="btn btn-ghost" onClick={onClose}>
          Fermer
        </button>
      </div>
      {join.node.isLocal ? (
        <p>
          L’agent de cette machine va se connecter tout seul dans quelques secondes : rien à faire. S’il n’apparaît pas en
          ligne, vérifiez qu’il tourne avec <code>docker compose ps</code>.
        </p>
      ) : (
        <>
          <p className="muted">
            Rien à installer à part Docker : l’agent Forgeyard est une image Docker, téléchargée et lancée automatiquement.
          </p>
          <ol className="steps-help">
            <li>
              Sur la machine à ajouter, lancez cette commande dans un terminal :
              <CopyField value={join.dockerCommand} />
            </li>
            <li>Le node apparaît en ligne ci-dessous en quelques secondes. L’agent redémarre tout seul avec la machine.</li>
          </ol>
          <p className="muted">
            Cette commande ne sert qu’une fois et expire à {expires}. Elle n’est plus jamais affichée : en cas de besoin,
            générez-en une nouvelle depuis la carte du node.
          </p>
        </>
      )}
    </div>
  )
}

function Bar({ used, total, label }: { used: number; total: number; label: string }) {
  const pct = total > 0 ? Math.min(100, (used / total) * 100) : 0
  const tone = pct >= 90 ? 'down' : pct >= 75 ? 'warn' : 'ok'
  return (
    <div className="meter">
      <div className="meter-label">
        <span>{label}</span>
        <span className="muted">
          {formatBytes(used)} / {formatBytes(total)}
        </span>
      </div>
      <div className="meter-track" role="meter" aria-label={label} aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
        <div className={`meter-fill meter-${tone}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

function NodeCard({ node, onJoin, onChange }: { node: Node; onJoin: (j: JoinCommand) => void; onChange: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const m = node.metrics

  async function run(action: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await action()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const stateLabel = { pending: 'En attente', online: 'En ligne', offline: 'Hors ligne' }[node.state]
  const dot = { pending: '', online: 'dot-up', offline: 'dot-down' }[node.state]

  return (
    <div className="panel node-card">
      <div className="node-head">
        <span className={`dot ${dot}`} />
        <strong className="node-name">{node.name}</strong>
        {node.isLocal && <span className="muted">cette machine</span>}
        <span className="muted">{stateLabel}</span>
      </div>

      {node.state === 'pending' ? (
        <p className="muted">La machine n'a pas encore rejoint le PaaS.</p>
      ) : (
        <>
          <p className="muted node-specs">
            {node.hostname} · {node.os} · {node.arch} · {node.cpus} CPU
            {node.dockerVersion ? ` · Docker ${node.dockerVersion}` : ' · Docker injoignable'}
          </p>
          {m ? (
            <>
              <div className="meter">
                <div className="meter-label">
                  <span>CPU</span>
                  <span className="muted">{m.cpuPercent.toFixed(0)} %</span>
                </div>
                <div className="meter-track" role="meter" aria-label="CPU" aria-valuenow={Math.round(m.cpuPercent)} aria-valuemin={0} aria-valuemax={100}>
                  <div className={`meter-fill meter-${m.cpuPercent >= 90 ? 'down' : m.cpuPercent >= 75 ? 'warn' : 'ok'}`} style={{ width: `${Math.min(100, m.cpuPercent)}%` }} />
                </div>
              </div>
              <Bar label="RAM" used={m.memoryUsedBytes} total={node.memoryBytes} />
              <Bar label="Disque" used={m.diskUsedBytes} total={node.diskBytes} />
              <p className="muted">
                {m.containersRunning} conteneur{m.containersRunning > 1 ? 's' : ''} en cours
              </p>
            </>
          ) : (
            <p className="muted">Vu pour la dernière fois {since(node.lastSeenAt)}.</p>
          )}
        </>
      )}

      {node.state !== 'pending' && <IngressSettings node={node} onSaved={onChange} />}

      {error && <p className="error">{error}</p>}
      <div className="panel-footer">
        {node.state === 'pending' && (
          <button type="button" className="btn" disabled={busy} onClick={() => run(async () => onJoin(await api.newJoinCommand(node.id)))}>
            {node.isLocal ? 'Relancer la connexion' : 'Nouvelle commande'}
          </button>
        )}
        <span className="spacer" />
        <button
          type="button"
          className="btn btn-danger"
          disabled={busy}
          onClick={() => {
            if (window.confirm(`Retirer le node « ${node.name} » ? Son agent sera déconnecté et ne pourra plus revenir.`)) {
              run(async () => {
                await api.deleteNode(node.id)
                onChange()
              })
            }
          }}
        >
          Retirer
        </button>
      </div>
    </div>
  )
}

/** How the node receives the web traffic of its apps. */
function IngressSettings({ node, onSaved }: { node: Node; onSaved: () => void }) {
  const [open, setOpen] = useState(false)
  const [ip, setIp] = useState(node.publicIp)
  const [mode, setMode] = useState(node.ingressMode)
  const [port, setPort] = useState(String(node.ingressHttpPort))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.setNodeIngress(node.id, { publicIp: ip, ingressMode: mode, ingressHttpPort: Number(port) })
      setOpen(false)
      onSaved()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const summary =
    node.ingressMode === 'traefik' ? 'Traefik sur 80/443, HTTPS automatique' : `Derrière un proxy, port ${node.ingressHttpPort}`

  if (!open) {
    return (
      <div className="ingress-summary">
        <span className="muted">
          Réseau : {summary}
          {node.publicIp ? ` · IP ${node.publicIp}` : ''}
        </span>
        <button type="button" className="btn btn-ghost" onClick={() => setOpen(true)}>
          Modifier
        </button>
      </div>
    )
  }

  return (
    <form className="form" onSubmit={save}>
      <label className="field">
        <span>IP publique</span>
        <input value={ip} onChange={(e) => setIp(e.target.value)} placeholder="Celle des Réglages par défaut" />
        <small>L’adresse vers laquelle pointent les domaines des apps de ce node.</small>
      </label>
      <label className="field">
        <span>Trafic web</span>
        <select value={mode} onChange={(e) => setMode(e.target.value as Node['ingressMode'])}>
          <option value="traefik">Traefik sur 80/443, HTTPS automatique</option>
          <option value="proxy">Derrière mon proxy (Caddy, Nginx…)</option>
        </select>
      </label>
      {mode === 'proxy' && (
        <>
          <label className="field">
            <span>Port HTTP</span>
            <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
          </label>
          <p className="muted">
            À ajouter une seule fois dans votre Caddyfile : ensuite chaque nouvelle app marche toute seule. Remplacez{' '}
            <code>IP</code> par l’IP locale de cette machine (<code>hostname -I</code>) et le domaine par le vôtre, puis
            rechargez Caddy.
          </p>
          <div className="field">
            <span>Si votre Caddy a le module DNS de votre fournisseur (ligne acme_dns)</span>
            <CopyField value={`*.mondomaine.com {\n    reverse_proxy IP:${port}\n}`} />
            <small>Un seul certificat wildcard pour toutes les apps.</small>
          </div>
          <div className="field">
            <span>Sinon (Caddy standard)</span>
            <CopyField
              value={`{\n    on_demand_tls {\n        ask http://IP:8080/api/caddy/ask\n    }\n}\n\nhttps:// {\n    tls {\n        on_demand\n    }\n    reverse_proxy IP:${port}\n}`}
            />
            <small>
              Un certificat par app, créé à sa première visite, seulement si c’est une vraie app. Si votre Caddyfile a déjà un
              bloc <code>{'{ … }'}</code> tout en haut, mettez <code>on_demand_tls</code> dedans au lieu d’en créer un second.
            </small>
          </div>
        </>
      )}
      {error && <p className="error">{error}</p>}
      <div className="panel-footer">
        <span className="spacer" />
        <button type="button" className="btn" onClick={() => setOpen(false)} disabled={busy}>
          Annuler
        </button>
        <button type="submit" className="btn btn-primary" disabled={busy}>
          Enregistrer
        </button>
      </div>
    </form>
  )
}

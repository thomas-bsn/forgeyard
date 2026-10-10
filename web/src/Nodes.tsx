import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { api, errorMessage, type App, type Container, type JoinCommand, type Node } from './api'
import { CopyField, formatBytes, Modal, ProxySnippet, since, Switch } from './ui'

const POLL_MS = 5000

export default function Nodes({ localSupported }: { localSupported: boolean }) {
  const [nodes, setNodes] = useState<Node[] | null>(null)
  const [apps, setApps] = useState<App[]>([])
  const [containers, setContainers] = useState<Container[]>([])
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [join, setJoin] = useState<JoinCommand | null>(null)
  const [network, setNetwork] = useState<Node | null>(null)
  const [agents, setAgents] = useState<{ autoUpdate: boolean; serverVersion: string } | null>(null)
  useEffect(() => {
    api.agentSettings().then(setAgents, () => {})
  }, [])

  async function load() {
    try {
      const [n, a, c] = await Promise.all([api.nodes(), api.apps(), api.containers()])
      setNodes(n)
      setApps(a)
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
  }, [])

  const online = nodes?.filter((n) => n.state === 'online').length ?? 0
  const joined = nodes?.filter((n) => n.state !== 'pending').length ?? 0

  return (
    <div className="section">
      <div className="toolbar">
        <h1 className="toolbar-title">
          Nodes{' '}
          <span className="muted">
            {online} en ligne sur {joined}
          </span>
        </h1>
        {agents && (
          <label className="toolbar-switch" title={`Les agents suivent la version du serveur (${shortVersion(agents.serverVersion)})`}>
            <span className="muted">Mise à jour auto des agents</span>
            <Switch
              checked={agents.autoUpdate}
              onChange={async (v) => setAgents(await api.saveAgentSettings(v).catch(() => agents))}
              label="Mise à jour automatique des agents"
            />
          </label>
        )}
        <button type="button" className="btn btn-primary" onClick={() => setAdding(true)}>
          + Ajouter un node
        </button>
      </div>
      {error && <p className="error">{error}</p>}
      {nodes && nodes.length === 0 && (
        <div className="empty-state">
          <strong>Aucun node</strong>
          <span>Un node est une machine qui fait tourner les apps. Ajoutez-en un pour commencer.</span>
        </div>
      )}
      {nodes && nodes.length > 0 && (
        <div className="node-grid">
          {nodes.map((n) => (
            <NodeCard
              key={n.id}
              node={n}
              sharesBox={sharesBox(n, nodes)}
              apps={apps.filter((a) => a.nodeId === n.id)}
              externals={containers.filter((c) => c.nodeId === n.id)}
              onJoin={setJoin}
              onNetwork={() => setNetwork(n)}
              onChange={load}
            />
          ))}
        </div>
      )}
      {adding && nodes && (
        <AddNode
          localAvailable={localSupported && !nodes.some((n) => n.isLocal)}
          onClose={() => setAdding(false)}
          onCreated={(j) => {
            setAdding(false)
            setJoin(j)
            load()
          }}
        />
      )}
      {join && <JoinInstructions join={join} lanIp={nodes?.find((n) => n.isLocal)?.localIp} onClose={() => setJoin(null)} />}
      {network && (
        <NetworkSettings
          node={network}
          sharesBox={sharesBox(network, nodes ?? [])}
          onClose={() => setNetwork(null)}
          onSaved={() => {
            setNetwork(null)
            load()
          }}
        />
      )}
    </div>
  )
}

/**
 * Whether a node sits behind the same public IP as Forgeyard's machine (a home box): it is then reached
 * through Forgeyard's machine, which relays its apps to it over the local network.
 */
function sharesBox(n: Node, all: Node[]): boolean {
  const front = all.find((x) => x.isLocal)
  return !!front && !n.isLocal && (n.publicIp === '' || n.publicIp === front.publicIp)
}

function AddNode({ localAvailable, onCreated, onClose }: { localAvailable: boolean; onCreated: (j: JoinCommand) => void; onClose: () => void }) {
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
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      title="Ajouter un node"
      subtitle="Une machine avec Docker qui fera tourner des apps."
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? 'Création…' : local ? 'Activer cette machine' : 'Créer'}
          </button>
        </>
      }
    >
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
      {!local && (
        <label className="field">
          <span>Nom du node</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="serveur-b"
            pattern="[a-z0-9][a-z0-9\-]{0,31}"
            title="1 à 32 caractères : a-z, 0-9 et -"
            required
            autoFocus
          />
        </label>
      )}
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

function JoinInstructions({ join, lanIp, onClose }: { join: JoinCommand; lanIp?: string; onClose: () => void }) {
  const expires = new Date(join.expiresAt * 1000).toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' })
  const [sameNetwork, setSameNetwork] = useState(false)
  const port = join.agentServer.slice(join.agentServer.lastIndexOf(':') + 1)
  // On the server's local network, the agent dials it directly, without going through the internet.
  const lanServer = lanIp ? `${lanIp}:${port}` : ''
  const command = sameNetwork && lanServer ? `${join.dockerCommand} --agent-server ${lanServer}` : join.dockerCommand
  return (
    <Modal
      title={join.node.isLocal ? 'Cette machine' : `Connecter « ${join.node.name} »`}
      onClose={onClose}
      wide
      footer={
        <button type="button" className="btn btn-primary" onClick={onClose}>
          Terminé
        </button>
      }
    >
      {join.node.isLocal ? (
        <p>
          L’agent de cette machine va se connecter tout seul dans quelques secondes : rien à faire. S’il n’apparaît pas en ligne,
          vérifiez qu’il tourne avec <code>docker compose ps</code>.
        </p>
      ) : (
        <>
          <p className="muted">Rien à installer à part Docker : l’agent Forgeyard est une image Docker, téléchargée et lancée automatiquement.</p>
          {lanServer && (
            <label className="check">
              <input type="checkbox" checked={sameNetwork} onChange={(e) => setSameNetwork(e.target.checked)} />
              <span>
                <b>Cette machine est sur le même réseau local que Forgeyard</b>
                <small>L’agent joindra Forgeyard directement en {lanServer}, sans passer par internet.</small>
              </span>
            </label>
          )}
          <ol className="steps-help">
            <li>
              Sur la machine à ajouter, lancez cette commande dans un terminal :
              <CopyField value={command} />
            </li>
            <li>Le node apparaît en ligne en quelques secondes. L’agent redémarre tout seul avec la machine.</li>
          </ol>
          {!sameNetwork && (
            <p className="muted">
              L’agent se connecte à <code>{join.agentServer}</code> : ce port doit être joignable depuis la machine. Avec Cloudflare,
              l’enregistrement de Forgeyard doit être en « DNS only » (le proxy orange ne transporte que le HTTPS), et le port{' '}
              {port} redirigé sur votre box si la machine est ailleurs.
            </p>
          )}
          <p className="muted">
            Cette commande ne sert qu’une fois et expire à {expires}. Elle n’est plus jamais affichée : en cas de besoin, générez-en une
            nouvelle depuis la carte du node.
          </p>
        </>
      )}
    </Modal>
  )
}

function Meter({ label, used, total, pct: rawPct, text }: { label: string; used?: number; total?: number; pct?: number; text?: string }) {
  const pct = Math.min(100, rawPct ?? (total ? ((used ?? 0) / total) * 100 : 0))
  const tone = pct >= 90 ? 'down' : pct >= 75 ? 'warn' : 'ok'
  return (
    <div className="meter">
      <div className="meter-label">
        <span>{label}</span>
        <span className={tone === 'ok' ? 'muted' : `text-${tone}`}>{text ?? `${formatBytes(used ?? 0)} / ${formatBytes(total ?? 0)}`}</span>
      </div>
      <div className="meter-track" role="meter" aria-label={label} aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
        <div className={`meter-fill meter-${tone}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

function NodeCard({
  node,
  sharesBox,
  apps,
  externals,
  onJoin,
  onNetwork,
  onChange,
}: {
  node: Node
  sharesBox: boolean
  apps: App[]
  externals: Container[]
  onJoin: (j: JoinCommand) => void
  onNetwork: () => void
  onChange: () => void
}) {
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
  const tone = { pending: '', online: 'up', offline: 'down' }[node.state]
  const appsOnline = apps.filter((a) => a.state === 'running').length
  const externalsRunning = externals.filter((c) => c.state === 'running').length

  return (
    <article className="panel node-card">
      <div className="node-head">
        <span className={`dot ${tone ? `dot-${tone}` : ''}`} />
        <h2 className="node-name">{node.name}</h2>
        {node.isLocal && <span className="badge badge-accent">cette machine</span>}
        <span className={`node-state ${tone ? `text-${tone}` : 'muted'}`}>{stateLabel}</span>
      </div>

      {node.state === 'pending' ? (
        <p className="muted">La machine n’a pas encore rejoint Forgeyard.</p>
      ) : (
        <>
          <p className="muted node-specs">
            {node.hostname} · {node.os} · {node.arch} · {node.cpus} CPU
            {node.dockerVersion ? ` · Docker ${node.dockerVersion}` : ''}
            {!m && <> · vu {since(node.lastSeenAt)}</>}
          </p>
          {node.state === 'online' && !node.dockerVersion && (
            <div className="banner banner-down">
              <span className="dot dot-down" />
              <span>
                L’agent n’arrive pas à joindre Docker sur cette machine{node.dockerError ? ` : ${node.dockerError}` : '.'} Vérifiez que le
                socket est monté (<code>-v /var/run/docker.sock:/var/run/docker.sock</code>) puis redémarrez l’agent.
              </span>
            </div>
          )}
          <AgentLine
            node={node}
            busy={busy}
            onUpdate={() =>
              run(async () => {
                await api.updateAgent(node.id)
                onChange()
              })
            }
          />
          {/* Offline, the same meters stay in place, empty, so cards keep the same layout. */}
          <div className={`meters-row ${m ? '' : 'meters-off'}`}>
            <Meter label="CPU" pct={m?.cpuPercent ?? 0} text={m ? `${m.cpuPercent.toFixed(0)} %` : '–'} />
            <Meter label="RAM" used={m?.memoryUsedBytes ?? 0} total={node.memoryBytes} text={m ? undefined : '–'} />
            <Meter label="Disque" used={m?.diskUsedBytes ?? 0} total={node.diskBytes} text={m ? undefined : '–'} />
          </div>
          <div className="tiles tiles-2">
            <div className="tile">
              <div className="k">Apps</div>
              <div className="v">
                {apps.length} <small>{apps.length ? `dont ${appsOnline} en ligne` : ''}</small>
              </div>
            </div>
            <div className="tile">
              <div className="k">Conteneurs en cours</div>
              <div className="v">{m ? appsOnline + externalsRunning : '–'}</div>
            </div>
          </div>
          {sharesBox && node.ingressMode === 'traefik' && (
            <div className="banner banner-warn">
              <span className="dot dot-warn" />
              <span>
                Ce node est derrière la même box que Forgeyard : ses apps n’y arrivent pas. Dans « Réseau… », choisissez « Via la machine
                de Forgeyard ».
              </span>
            </div>
          )}
          <div className="network-line">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="9" />
              <path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18" />
            </svg>
            <div>
              <strong>
                {node.ingressMode === 'traefik'
                  ? 'Forgeyard gère les ports 80 et 443'
                  : sharesBox
                    ? 'Apps relayées par la machine de Forgeyard'
                    : 'Derrière votre reverse proxy'}
              </strong>
              <span className="muted">
                {node.ingressMode === 'traefik' ? 'HTTPS automatique' : `Apps reçues sur le port ${node.ingressHttpPort}`}
                {node.publicIp ? ` · IP publique ${node.publicIp}` : ''}
              </span>
            </div>
          </div>
        </>
      )}

      {error && <p className="error">{error}</p>}
      <div className="card-actions">
        {node.state === 'pending' ? (
          <button type="button" className="btn" disabled={busy} onClick={() => run(async () => onJoin(await api.newJoinCommand(node.id)))}>
            {node.isLocal ? 'Relancer la connexion' : 'Nouvelle commande'}
          </button>
        ) : (
          <button type="button" className="btn" onClick={onNetwork}>
            Réseau…
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
    </article>
  )
}

/** How the node receives the web traffic of its apps. */
function NetworkSettings({ node, sharesBox, onClose, onSaved }: { node: Node; sharesBox: boolean; onClose: () => void; onSaved: () => void }) {
  const [ip, setIp] = useState(node.publicIp)
  const [mode, setMode] = useState(node.ingressMode)
  const [port, setPort] = useState(String(node.ingressHttpPort))
  const [domain, setDomain] = useState('')
  const [defaultIp, setDefaultIp] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    // Only the superadmin may read the domain settings; others see a placeholder domain in the example.
    api.domainSettings().then(
      (d) => {
        setDomain(d.domain)
        setDefaultIp(d.publicIp)
      },
      () => {},
    )
  }, [])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.setNodeIngress(node.id, { publicIp: ip, ingressMode: mode, ingressHttpPort: Number(port) })
      onSaved()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      title={`Réseau de « ${node.name} »`}
      onClose={onClose}
      onSubmit={save}
      wide
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            Enregistrer
          </button>
        </>
      }
    >
      <div className="field">
        <span>{sharesBox ? 'Comment ce node reçoit le trafic de ses apps ?' : 'Qui gère les ports 80 et 443 de cette machine ?'}</span>
        <div className="auth-options">
          <button type="button" className={`auth-option ${mode === 'traefik' ? 'selected' : ''}`} onClick={() => setMode('traefik')} aria-pressed={mode === 'traefik'}>
            <strong>{sharesBox ? 'Directement sur ce node' : 'Forgeyard'}</strong>
            <small>
              {sharesBox
                ? 'Ce node prend les ports 80 et 443 : il faudrait que la box lui envoie le trafic.'
                : 'HTTPS automatique, rien à configurer.'}
            </small>
          </button>
          <button type="button" className={`auth-option ${mode === 'proxy' ? 'selected' : ''}`} onClick={() => setMode('proxy')} aria-pressed={mode === 'proxy'}>
            {sharesBox ? (
              <>
                <strong>Via la machine de Forgeyard</strong>
                <small>Elle reçoit le trafic de la box et passe à ce node ses apps, sur votre réseau local.</small>
              </>
            ) : (
              <>
                <strong>Mon reverse proxy</strong>
                <small>Caddy, Nginx… garde 80/443 et envoie les apps à Forgeyard.</small>
              </>
            )}
          </button>
        </div>
      </div>
      {sharesBox && (
        <p className="hint">
          Ce node est derrière la même box que Forgeyard : choisissez « Via la machine de Forgeyard ». Elle reçoit le trafic et relaie à
          ce node ses apps sur votre réseau local{node.localIp ? ` (${node.localIp})` : ''} : rien à installer ni à configurer.
        </p>
      )}
      {mode === 'proxy' && (
        <>
          <label className="field">
            <span>Port d’entrée HTTP</span>
            <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
            <small>Le port de cette machine où Forgeyard reçoit les apps. Laissez 8090 sauf s’il est déjà pris.</small>
          </label>
          {!sharesBox && <ProxySnippet domain={domain} port={Number(port) || 8090} detectedIp={node.localIp} />}
        </>
      )}
      <label className="field">
        <span>IP publique de ce node</span>
        <input value={ip} onChange={(e) => setIp(e.target.value)} placeholder={defaultIp ? `${defaultIp} (celle de Réglages › Domaine)` : 'Celle de Réglages › Domaine'} />
        <small>
          L’IP vers laquelle Forgeyard fait pointer le DNS des apps de ce node. Laissez vide si ce node est derrière la même box que
          {defaultIp ? ` ${defaultIp}` : ' celle des réglages'} ; remplissez-la seulement s’il est ailleurs (un VPS, une autre maison…).
        </small>
      </label>
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

/** The commit as people read it. */
function shortVersion(v: string): string {
  return v ? v.slice(0, 7) : 'dev'
}

/** The agent's version against the server's, with its update. */
function AgentLine({ node, busy, onUpdate }: { node: Node; busy: boolean; onUpdate: () => void }) {
  if (node.state !== 'online') return null
  let status: ReactNode
  if (node.updating) status = <span className="text-warn">mise à jour…</span>
  else if (!node.agentOutdated) status = <span className="text-up">à jour</span>
  else if (node.selfUpdate)
    status = (
      <button type="button" className="link-button" disabled={busy} onClick={onUpdate}>
        Mettre à jour
      </button>
    )
  else status = <span className="muted">{node.isLocal ? 'se met à jour avec docker compose' : 'à mettre à jour à la main'}</span>
  return (
    <p className="agent-line muted">
      Agent {shortVersion(node.agentVersion)} · {status}
      {node.updateError && <span className="error agent-error">Échec de la mise à jour : {node.updateError}</span>}
    </p>
  )
}

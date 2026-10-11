import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { api, errorMessage, type App, type Container, type JoinCommand, type Node } from './api'
import { CopyField, formatBytes, Modal, ProxySnippet, since, Switch } from './ui'
import { TopologyMap, TopologyTable } from './Network'
import { Logs } from './Apps'

const POLL_MS = 5000
// How long a removed node's card takes to fade out (the .node-gone animation).
const REMOVAL_MS = 400

type View = 'cards' | 'map' | 'table'

const views: [View, string][] = [
  ['cards', 'Cartes'],
  ['map', 'Topologie'],
  ['table', 'Tableau'],
]

// The chosen view is remembered by the browser.
function storedView(): View {
  try {
    const v = localStorage.getItem('forgeyard.nodesView')
    if (v === 'map' || v === 'table') return v
  } catch {
    // storage blocked: the cards
  }
  return 'cards'
}

export default function Nodes({ localSupported }: { localSupported: boolean }) {
  const [nodes, setNodes] = useState<Node[] | null>(null)
  const [apps, setApps] = useState<App[]>([])
  const [containers, setContainers] = useState<Container[]>([])
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [join, setJoin] = useState<JoinCommand | null>(null)
  const [network, setNetwork] = useState<Node | null>(null)
  const [view, setView] = useState<View>(storedView)
  function pickView(v: View) {
    setView(v)
    try {
      localStorage.setItem('forgeyard.nodesView', v)
    } catch {
      // not remembered
    }
  }
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
        <div className="segmented" role="tablist" aria-label="Vue des nodes">
          {views.map(([v, label]) => (
            <button key={v} type="button" role="tab" aria-selected={view === v} className={view === v ? 'on' : ''} onClick={() => pickView(v)}>
              {label}
            </button>
          ))}
        </div>
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
      {view === 'map' && nodes && nodes.length > 0 && <TopologyMap />}
      {view === 'table' && nodes && nodes.length > 0 && <TopologyTable />}
      {view === 'cards' && nodes && nodes.length > 0 && (
        <div className="node-grid">
          {nodes.map((n) => (
            <NodeCard
              key={n.id}
              node={n}
              local={nodes.find((x) => x.isLocal)}
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
          front={nodes.find((n) => n.isLocal)}
          onClose={() => {
            setAdding(false)
            load()
          }}
          onDone={() => {
            setAdding(false)
            load()
          }}
        />
      )}
      {join && <JoinInstructions join={join} lanIp={nodes?.find((n) => n.isLocal)?.localIp} onClose={() => setJoin(null)} />}
      {network && (
        <NetworkSettings
          node={network}
          local={(nodes ?? []).find((x) => x.isLocal)}
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
type Place = 'this' | 'lan' | 'remote'
type NodeCheck = { connected: boolean; traefik: 'ok' | 'down' | 'unknown'; reach: 'ok' | 'error' | 'skip'; reachTarget?: string; reachDetail?: string }

const steps = ['Nom', 'Où est la machine', 'Devant les apps', 'Installer l’agent']

/**
 * Adding a node, step by step: its name, where the machine is (Forgeyard's own, on its network, or
 * elsewhere), who receives its apps' visits, then the agent's install command and the checks that it
 * is ready.
 */
function AddNode({ localAvailable, front, onDone, onClose }: { localAvailable: boolean; front?: Node; onDone: () => void; onClose: () => void }) {
  const [step, setStep] = useState(1)
  const [name, setName] = useState('')
  const [place, setPlace] = useState<Place>(localAvailable ? 'this' : 'lan')
  const [ingress, setIngress] = useState<Ingress>(front ? 'relay' : 'traefik')
  const [publicIp, setPublicIp] = useState('')
  const [port, setPort] = useState('8090')
  const [domain, setDomain] = useState('')
  const [join, setJoin] = useState<JoinCommand | null>(null)
  const [node, setNode] = useState<Node | null>(null)
  const [check, setCheck] = useState<NodeCheck | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const frontName = front?.name ?? 'la machine de Forgeyard'

  useEffect(() => {
    api.instance().then((i) => setDomain(i.appsDomain ?? ''), () => {})
  }, [])

  // The second question depends on the first: on Forgeyard's network, its machine can relay.
  function pickPlace(p: Place) {
    setPlace(p)
    setIngress(p === 'lan' && front ? 'relay' : 'traefik')
  }

  // Once the command is shown, wait for the agent, then check the node is ready.
  useEffect(() => {
    if (step !== 4 || !join) return
    let live = true
    const tick = async () => {
      try {
        const n = (await api.nodes()).find((x) => x.id === join.node.id) ?? null
        if (!live) return
        setNode(n)
        if (n?.state === 'online') setCheck(await api.checkNode(n.id))
      } catch {
        // tried again at the next tick
      }
    }
    tick()
    const t = setInterval(tick, 3000)
    return () => {
      live = false
      clearInterval(t)
    }
  }, [step, join])

  async function create() {
    setBusy(true)
    setError('')
    try {
      const local = place === 'this'
      const j = await api.createNode({
        name: local ? 'local' : name,
        local,
        lan: place === 'lan',
        ingressMode: ingress,
        publicIp: place === 'remote' ? publicIp : '',
        ingressHttpPort: Number(port) || 8090,
      })
      setJoin(j)
      setStep(4)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const options: { key: Ingress; title: string; text: string; flow: string; recommended?: boolean }[] =
    place === 'this'
      ? [
          { key: 'traefik', title: 'Traefik de Forgeyard', text: 'Il prend les ports 80 et 443 de cette machine et gère le HTTPS. Rien à configurer.', flow: 'Internet → Traefik :443 → apps', recommended: true },
          { key: 'proxy', title: 'Mon reverse proxy', text: 'Ton Caddy, Nginx… garde 80/443 et envoie les apps au Traefik de Forgeyard. Une règle à ajouter.', flow: `Internet → ton proxy :443 → Traefik :${port} → apps` },
        ]
      : place === 'lan'
        ? [
            front
              ? { key: 'relay', title: `Traefik de ${frontName}`, text: `Les visites arrivent sur ${frontName}, qui les relaie à ce node par le réseau local. Rien à configurer.`, flow: `box → ${frontName} :443 → ce node :${port} → apps`, recommended: true }
              : { key: 'traefik', title: 'Traefik de l’agent', text: 'Il prend les ports 80 et 443 de cette machine : la box doit les lui envoyer.', flow: 'box → Traefik :443 → apps' },
            { key: 'proxy', title: 'Mon reverse proxy', text: 'Ton Caddy (sur une autre machine ou celle-ci) envoie lui-même les apps au Traefik de ce node. Une règle à ajouter.', flow: `box → ton proxy :443 → ce node :${port} → apps` },
          ]
        : [
            { key: 'traefik', title: 'Traefik de l’agent', text: 'Il prend les ports 80 et 443 de la machine et gère le HTTPS. Rien à configurer.', flow: 'Internet → Traefik :443 → apps', recommended: true },
            { key: 'proxy', title: 'Mon reverse proxy', text: 'Ton Caddy, Nginx… garde 80/443 et envoie les apps au Traefik de l’agent. Une règle à ajouter.', flow: `Internet → ton proxy :443 → Traefik :${port} → apps` },
          ]
  const placeText = { this: 'cette machine', lan: 'dans ton réseau', remote: 'ailleurs' }[place]
  const recap = [place === 'this' ? 'Cette machine' : `« ${name} »`, step > 2 && placeText, step > 3 && options.find((o) => o.key === ingress)?.title]
    .filter(Boolean)
    .join(' · ')
  const canNext = step === 1 ? /^[a-z0-9][a-z0-9-]{0,31}$/.test(name) || place === 'this' : true
  const ready = !!check && check.connected && check.traefik !== 'down' && check.reach !== 'error'

  return (
    <Modal
      title="Ajouter un node"
      onClose={onClose}
      onSubmit={(e: FormEvent) => {
        e.preventDefault()
        if (step < 3 && canNext) setStep(step + 1)
        else if (step === 3) create()
      }}
      wide
      footer={
        <>
          {step > 1 && step < 4 ? (
            <button type="button" className="btn" onClick={() => setStep(step - 1)} disabled={busy}>
              ← Retour
            </button>
          ) : (
            <button type="button" className="btn" onClick={step === 4 ? onDone : onClose}>
              {step === 4 ? 'Fermer' : 'Annuler'}
            </button>
          )}
          <span className="spacer" />
          {step < 3 && (
            <button type="submit" className="btn btn-primary" disabled={!canNext}>
              Continuer →
            </button>
          )}
          {step === 3 && (
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {busy ? 'Création…' : place === 'this' ? 'Activer cette machine' : 'Créer et obtenir la commande'}
            </button>
          )}
          {step === 4 && ready && (
            <button type="button" className="btn btn-primary" onClick={onDone}>
              Terminer
            </button>
          )}
        </>
      }
    >
      <div className="wizard-steps">
        {steps.map((s, i) => (
          <span key={s} className={`wizard-step ${i + 1 < step ? 'done' : ''} ${i + 1 === step ? 'on' : ''}`}>
            <i>{i + 1 < step ? '✓' : i + 1}</i>
            {s}
          </span>
        ))}
      </div>
      {step > 1 && <p className="muted wizard-recap">{recap}</p>}

      {step === 1 && (
        <>
          {localAvailable && (
            <div className="auth-options">
              <button type="button" className={`auth-option ${place === 'this' ? 'selected' : ''}`} onClick={() => pickPlace('this')} aria-pressed={place === 'this'}>
                <strong>Cette machine</strong>
                <small>Celle où tourne Forgeyard : son agent est déjà là, rien à installer.</small>
              </button>
              <button type="button" className={`auth-option ${place !== 'this' ? 'selected' : ''}`} onClick={() => pickPlace('lan')} aria-pressed={place !== 'this'}>
                <strong>Une autre machine</strong>
                <small>Un autre serveur avec Docker.</small>
              </button>
            </div>
          )}
          {place !== 'this' && (
            <label className="field">
              <span>Comment s’appelle cette machine ?</span>
              <input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="raspberry-pi" pattern="[a-z0-9][a-z0-9\-]{0,31}" required autoFocus />
              <small>Juste pour toi : il apparaît sur la page Nodes et dans la topologie. Lettres, chiffres et tirets.</small>
            </label>
          )}
        </>
      )}

      {step === 2 &&
        (place === 'this' ? (
          <p className="hint">C’est la machine de Forgeyard : elle reçoit elle-même les visites. Passe à l’étape suivante.</p>
        ) : (
          <div className="field">
            <span>Où est la machine ?</span>
            <div className="auth-options">
              <button type="button" className={`auth-option ${place === 'lan' ? 'selected' : ''}`} onClick={() => pickPlace('lan')} aria-pressed={place === 'lan'}>
                <strong>🏠 Dans mon réseau</strong>
                <small>
                  Derrière la même box que {frontName}
                  {front?.localIp ? ` (${front.localIp})` : ''}. Rien à ouvrir sur la box.
                </small>
              </button>
              <button type="button" className={`auth-option ${place === 'remote' ? 'selected' : ''}`} onClick={() => pickPlace('remote')} aria-pressed={place === 'remote'}>
                <strong>🌍 Ailleurs</strong>
                <small>Un VPS, une autre maison, un bureau : elle a sa propre IP publique.</small>
              </button>
            </div>
          </div>
        ))}

      {step === 3 && (
        <>
          <div className="field">
            <span>Qui reçoit les visites de ses apps ?</span>
            <div className="auth-options">
              {options.map((o) => (
                <button key={o.key} type="button" className={`auth-option ${ingress === o.key ? 'selected' : ''}`} onClick={() => setIngress(o.key)} aria-pressed={ingress === o.key}>
                  <strong>
                    {o.title} {o.recommended && <span className="badge badge-up">recommandé</span>}
                  </strong>
                  <small>{o.text}</small>
                  <small className="mono">{o.flow}</small>
                </button>
              ))}
            </div>
          </div>
          {place === 'remote' && (
            <label className="field">
              <span>IP publique de la machine</span>
              <input value={publicIp} onChange={(e) => setPublicIp(e.target.value.trim())} placeholder="51.210.33.7" required />
              <small>Le DNS de ses apps pointera ici.</small>
            </label>
          )}
          {ingress !== 'traefik' && (
            <label className="field field-narrow">
              <span>{ingress === 'relay' ? 'Port du Traefik de ce node pour le relais' : 'Port du Traefik, vers lequel ton proxy envoie les apps'}</span>
              <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
              <small>Garde 8090 sauf s’il est déjà pris sur la machine.</small>
            </label>
          )}
          {ingress === 'proxy' && <ProxySnippet domain={domain} port={Number(port) || 8090} initialIp={place === 'lan' ? undefined : '127.0.0.1'} />}
          {error && <p className="error">{error}</p>}
        </>
      )}

      {step === 4 && join && (
        <>
          {join.node.isLocal ? (
            <p>L’agent de cette machine se connecte tout seul dans quelques secondes : rien à lancer.</p>
          ) : (
            <>
              <span className="muted">Lance cette commande sur « {join.node.name} », dans un terminal (elle ne sert qu’une fois, pendant une heure) :</span>
              <CopyField value={`sudo ${join.dockerCommand}`} />
              {join.node.lan && join.dockerCommand.includes('--agent-server') && (
                <small className="muted">
                  Ajouté pour toi : <code>--agent-server {join.agentServer}</code>, la machine joint {frontName} directement par le réseau local.
                </small>
              )}
              {!join.node.lan && (
                <small className="muted">
                  L’agent se connecte à <code>{join.agentServer}</code> : ce port doit être joignable depuis la machine (avec Cloudflare, le nom de
                  Forgeyard doit être en « DNS only »).
                </small>
              )}
            </>
          )}
          <ul className="check-list">
            <CheckItem state={node?.state === 'online' ? 'ok' : 'wait'} text={node?.state === 'online' ? `Agent connecté${node.os ? ` (${node.os} · ${node.arch} · ${node.cpus} CPU)` : ''}` : 'En attente de la connexion de l’agent…'} />
            {node?.state === 'online' && (
              <CheckItem
                state={!check || check.traefik === 'unknown' ? 'wait' : check.traefik === 'ok' ? 'ok' : 'error'}
                text={check?.traefik === 'down' ? 'Traefik ne tourne pas : regarde les logs de l’agent' : check?.traefik === 'ok' ? `Traefik lancé${ingress === 'traefik' ? ' sur 80 et 443' : ` sur le port ${port}`}` : 'Lancement de Traefik…'}
              />
            )}
            {node?.state === 'online' && check && check.reach !== 'skip' && (
              <CheckItem
                state={check.reach === 'ok' ? 'ok' : 'error'}
                text={check.reach === 'ok' ? `${ingress === 'relay' ? `${frontName} joint ce node` : 'Traefik joignable'} sur ${check.reachTarget}` : (check.reachDetail ?? 'injoignable')}
              />
            )}
            {node?.state === 'online' && check?.reach === 'skip' && check.reachDetail && <CheckItem state="info" text={check.reachDetail} />}
          </ul>
        </>
      )}
    </Modal>
  )
}

function CheckItem({ state, text }: { state: 'ok' | 'wait' | 'error' | 'info'; text: string }) {
  return (
    <li className={`check-item check-${state}`}>
      <span className="check-mark" aria-hidden="true">
        {state === 'ok' ? '✓' : state === 'error' ? '✕' : state === 'info' ? 'i' : ''}
      </span>
      {text}
    </li>
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
          {lanServer && !join.node.lan && (
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
  local,
  apps,
  externals,
  onJoin,
  onNetwork,
  onChange,
}: {
  node: Node
  local?: Node
  apps: App[]
  externals: Container[]
  onJoin: (j: JoinCommand) => void
  onNetwork: () => void
  onChange: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // removing: while its agent cleans the machine; gone: the card fades out before leaving the list.
  const [removal, setRemoval] = useState<'removing' | 'gone' | null>(null)
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

  // Away right after an update: its agent is restarting, not lost.
  const restarting = node.state === 'offline' && node.updating
  const stateLabel = restarting ? 'Mise à jour…' : { pending: 'En attente', online: 'En ligne', offline: 'Hors ligne' }[node.state]
  const tone = restarting ? 'warn' : { pending: '', online: 'up', offline: 'down' }[node.state]
  const [showLogs, setShowLogs] = useState(false)
  const appsOnline = apps.filter((a) => a.state === 'running').length
  const externalsRunning = externals.filter((c) => c.state === 'running').length

  return (
    <article className={`panel node-card${removal ? ` node-${removal}` : ''}`}>
      <div className="node-head">
        <span className={`dot ${tone ? `dot-${tone}` : ''}`} />
        <h2 className="node-name">{node.name}</h2>
        {node.isLocal && <span className="badge badge-accent">cette machine</span>}
        <span className={`node-state ${tone ? `text-${tone}` : 'muted'}`}>{removal ? 'Retrait…' : stateLabel}</span>
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
          {!node.isLocal && local && node.ingressMode === 'traefik' && (!node.publicIp || node.publicIp === local.publicIp) && (
            <div className="banner banner-warn">
              <span className="dot dot-warn" />
              <span>
                Ce node reçoit ses visites directement, mais partage l’IP publique de {local.name} : elles n’y arrivent pas. Dans « Réseau… »,
                choisissez « Relais par {local.name} » ou donnez-lui son IP publique.
              </span>
            </div>
          )}
          <div className="network-line">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <circle cx="12" cy="12" r="9" />
              <path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18" />
            </svg>
            <div>
              <strong>{ingressLine(node, local).title}</strong>
              <span className="muted">
                {ingressLine(node, local).text}
                {node.publicIp && node.ingressMode !== 'relay' ? ` · IP publique ${node.publicIp}` : ''}
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
          <>
            <button type="button" className="btn" onClick={onNetwork}>
              Réseau…
            </button>
            {node.state === 'online' && (
              <button type="button" className="btn" onClick={() => setShowLogs(true)} title="Logs de l’agent">
                Logs
              </button>
            )}
          </>
        )}
        <span className="spacer" />
        <button
          type="button"
          className="btn btn-danger"
          disabled={busy}
          onClick={() => {
            if (
              window.confirm(
                `Retirer le node « ${node.name} » ? Son agent supprime de la machine ce que Forgeyard y a mis (conteneurs et données des apps, Traefik, réseau), puis se désinstalle. Les autres conteneurs de la machine restent.`,
              )
            ) {
              run(async () => {
                setRemoval('removing')
                try {
                  const res = await api.deleteNode(node.id)
                  if (!res.cleaned) {
                    window.alert(`Node retiré, mais ${res.detail}. Les commandes pour nettoyer la machine à la main sont dans la doc : docs/nodes/README.md, « Retirer un node ».`)
                  }
                } catch (err) {
                  setRemoval(null)
                  throw err
                }
                setRemoval('gone')
                await new Promise((resolve) => setTimeout(resolve, REMOVAL_MS))
                onChange()
              })
            }
          }}
        >
          {removal ? 'Retrait…' : 'Retirer'}
        </button>
      </div>
      {showLogs && (
        <Modal title={`Logs de l’agent · ${node.name}`} subtitle="Depuis son dernier démarrage, en direct." onClose={() => setShowLogs(false)} wide>
          <Logs url={`/api/admin/nodes/${node.id}/logs`} />
        </Modal>
      )}
    </article>
  )
}

type Ingress = Node['ingressMode']

/** How the node receives the web traffic of its apps: directly, through Forgeyard's machine, or behind its own proxy. */
function NetworkSettings({ node, local, onClose, onSaved }: { node: Node; local?: Node; onClose: () => void; onSaved: () => void }) {
  const [ip, setIp] = useState(node.publicIp)
  const [mode, setMode] = useState<Ingress>(node.ingressMode)
  const [port, setPort] = useState(String(node.ingressHttpPort))
  const [domain, setDomain] = useState('')
  const [defaultIp, setDefaultIp] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const isFront = node.isLocal
  const front = local && !isFront ? local : undefined

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
      await api.setNodeIngress(node.id, { publicIp: mode === 'relay' ? '' : ip, ingressMode: mode, ingressHttpPort: Number(port) })
      onSaved()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  // Forgeyard's machine takes ports 80 and 443 of its box: a node behind the same IP cannot get them too.
  const frontIp = front ? front.publicIp || defaultIp : ''
  const sameIpAsFront = !!front && (ip.trim() === '' || ip.trim() === frontIp)

  const options: { key: Ingress; title: string; text: string; hidden?: boolean }[] = isFront
    ? [
        { key: 'traefik', title: 'Forgeyard', text: 'Traefik prend les ports 80 et 443 de cette machine et gère le HTTPS : rien à configurer.' },
        { key: 'proxy', title: 'Mon reverse proxy', text: 'Caddy, Nginx… garde 80/443 et envoie les apps au Traefik de Forgeyard.' },
      ]
    : [
        {
          key: 'relay',
          title: front ? `Relais par ${front.name}` : 'Relais par la machine de Forgeyard',
          text: front
            ? `Sur le même réseau que ${front.name} : les visites arrivent chez lui, qui les passe à ce node par le réseau local. Rien à configurer.`
            : 'Il faut d’abord activer la machine de Forgeyard comme node.',
        },
        { key: 'traefik', title: 'Directement', text: 'Ce node a sa propre IP publique (un VPS, une autre box) : son Traefik prend 80 et 443 et gère le HTTPS.' },
        { key: 'proxy', title: 'Son propre reverse proxy', text: 'Un Caddy ou un Nginx devant ce node lui envoie ses apps : à configurer chez toi.' },
      ]

  return (
    <Modal
      title={`Réseau de « ${node.name} »`}
      subtitle="Comment les visites de ses apps arrivent jusqu’à lui."
      onClose={onClose}
      onSubmit={save}
      wide
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || (mode === 'relay' && !front)}>
            Enregistrer
          </button>
        </>
      }
    >
      <div className={`auth-options ${options.length === 3 ? 'auth-options-3' : ''}`}>
        {options.map((o) => (
          <button
            key={o.key}
            type="button"
            className={`auth-option ${mode === o.key ? 'selected' : ''}`}
            onClick={() => setMode(o.key)}
            aria-pressed={mode === o.key}
            disabled={o.key === 'relay' && !front}
          >
            <strong>{o.title}</strong>
            <small>{o.text}</small>
          </button>
        ))}
      </div>

      {mode === 'relay' && front && (
        <>
          <label className="field">
            <span>Port de ce node pour le relais</span>
            <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
            <small>
              {front.name} envoie les apps à <span className="mono">{node.localIp || 'l’IP locale de ce node'}:{port || '8090'}</span>, en HTTP sur le réseau
              local. Laissez 8090 sauf s’il est déjà pris.
            </small>
          </label>
          <p className="hint">Le DNS des apps de ce node pointe vers {front.name}{frontIp ? ` (${frontIp})` : ''}, qui reçoit les visites et gère le HTTPS.</p>
        </>
      )}
      {mode === 'traefik' && !isFront && sameIpAsFront && (
        <div className="banner banner-warn">
          <span className="dot dot-warn" />
          <span>
            Sans IP publique à lui, ce node partage celle de {front!.name} : la box n’envoie 80 et 443 qu’à une seule machine, et les visites
            n’arriveraient pas ici. Indiquez son IP publique, ou choisissez « Relais par {front!.name} ».
          </span>
        </div>
      )}
      {mode === 'proxy' && (
        <>
          <label className="field">
            <span>Port d’entrée HTTP</span>
            <input type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} required />
            <small>Le port de cette machine où ton reverse proxy envoie les apps. Laissez 8090 sauf s’il est déjà pris.</small>
          </label>
          <ProxySnippet domain={domain} port={Number(port) || 8090} detectedIp={node.localIp} />
        </>
      )}
      {mode !== 'relay' && (
        <label className="field">
          <span>IP publique de ce node</span>
          <input value={ip} onChange={(e) => setIp(e.target.value)} placeholder={defaultIp ? `${defaultIp} (celle de Réglages › Domaine)` : 'Celle de Réglages › Domaine'} />
          <small>
            L’IP vers laquelle Forgeyard fait pointer le DNS des apps de ce node. Vide : celle de Réglages › Domaine
            {defaultIp ? ` (${defaultIp})` : ''}.
          </small>
        </label>
      )}
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

/** How a node receives its visits, in a line for its card. */
function ingressLine(node: Node, local?: Node): { title: string; text: string } {
  switch (node.ingressMode) {
    case 'relay':
      return { title: `Relayé par ${local?.name ?? 'la machine de Forgeyard'}`, text: `Apps reçues sur le port ${node.ingressHttpPort}, par le réseau local` }
    case 'proxy':
      return { title: 'Derrière son reverse proxy', text: `Apps reçues sur le port ${node.ingressHttpPort}` }
  }
  return { title: node.isLocal ? 'Forgeyard gère les ports 80 et 443' : 'Reçoit ses visites directement', text: 'HTTPS automatique' }
}

/** The commit as people read it. */
function shortVersion(v: string): string {
  return v ? v.slice(0, 7) : 'dev'
}

/** The agent's version against the server's, with its update. */
function AgentLine({ node, busy, onUpdate }: { node: Node; busy: boolean; onUpdate: () => void }) {
  if (node.state === 'offline' && node.updating) {
    return <p className="agent-line text-warn">L’agent redémarre avec sa nouvelle version : il revient dans un instant.</p>
  }
  if (node.state !== 'online') return null
  let status: ReactNode
  if (node.updating) status = <span className="text-warn">{node.updateStep === 'restart' ? 'redémarrage avec la nouvelle version…' : 'téléchargement de la nouvelle version…'}</span>
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

import { useEffect, useState } from 'react'
import { api, type Instance, type Node, type User } from './api'
import { Logo, ThemeToggle } from './ui'
import Requests from './Requests'
import Settings from './Settings'
import Nodes from './Nodes'

const roleLabels: Record<User['role'], string> = {
  superadmin: 'superadmin',
  admin: 'admin',
  user: 'utilisateur',
}

type Tab = 'apps' | 'nodes' | 'requests' | 'settings'

export default function Dashboard({ instance, user, onLogout }: { instance: Instance; user: User; onLogout: () => void }) {
  const admin = user.role === 'admin' || user.role === 'superadmin'
  const [tab, setTab] = useState<Tab>('apps')
  const [pending, setPending] = useState(0)

  useEffect(() => {
    if (admin) api.requests().then((r) => setPending(r.length)).catch(() => {})
  }, [admin])

  async function logout() {
    await api.logout()
    onLogout()
  }

  const tabButton = (value: Tab, label: string, count?: number) => (
    <button type="button" className={`chip ${tab === value ? 'on' : ''}`} onClick={() => setTab(value)} aria-pressed={tab === value}>
      {label}
      {count ? <span className="count">{count}</span> : null}
    </button>
  )

  return (
    <div className="page">
      <header className="header">
        <Logo size={36} />
        <div className="header-title">
          <h1>{instance.name}</h1>
          <p className="muted">
            Connecté en tant que {user.displayName} ({roleLabels[user.role]})
          </p>
        </div>
        <div className="header-actions">
          <ThemeToggle />
          <button type="button" className="btn" onClick={logout}>
            Déconnexion
          </button>
        </div>
      </header>

      <nav className="tabs" aria-label="Sections">
        {tabButton('apps', 'Apps')}
        {admin && tabButton('nodes', 'Nodes')}
        {admin && tabButton('requests', 'Demandes', pending)}
        {admin && tabButton('settings', 'Réglages')}
      </nav>

      {tab === 'apps' && <Apps admin={admin} />}
      {tab === 'nodes' && admin && <Nodes />}
      {tab === 'requests' && admin && <Requests onCountChange={setPending} />}
      {tab === 'settings' && admin && <Settings superadmin={user.role === 'superadmin'} />}
    </div>
  )
}

function Apps({ admin }: { admin: boolean }) {
  const [nodes, setNodes] = useState<Node[] | null>(null)
  useEffect(() => {
    if (admin) api.nodes().then(setNodes).catch(() => {})
  }, [admin])
  const online = nodes?.filter((n) => n.state === 'online').length

  return (
    <>
      <div className="stats">
        <div className="stat">
          <div className="k">Apps en ligne</div>
          <div className="v">
            0 <small>/ 0</small>
          </div>
        </div>
        <div className="stat">
          <div className="k">Suspendues</div>
          <div className="v">0</div>
        </div>
        <div className="stat">
          <div className="k">Nodes en ligne</div>
          <div className="v">
            {nodes ? (
              <>
                {online} <small>/ {nodes.filter((n) => n.state !== 'pending').length}</small>
              </>
            ) : (
              '–'
            )}
          </div>
        </div>
      </div>
      <div className="empty-state">
        <strong>Aucune app pour le moment</strong>
        <span>Le déploiement de conteneurs arrive avec l'agent, dans la prochaine étape.</span>
      </div>
    </>
  )
}

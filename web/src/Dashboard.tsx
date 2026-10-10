import { useEffect, useState } from 'react'
import { api, type Instance, type User } from './api'
import { Logo, ThemeToggle, useRoute } from './ui'
import Settings from './Settings'
import Nodes from './Nodes'
import Apps from './Apps'
import Users from './Users'

const roleLabels: Record<User['role'], string> = {
  superadmin: 'superadmin',
  admin: 'admin',
  user: 'utilisateur',
}

export default function Dashboard({
  instance,
  user,
  onLogout,
  onRefresh,
}: {
  instance: Instance
  user: User
  onLogout: () => void
  onRefresh: () => void
}) {
  const admin = user.role === 'admin' || user.role === 'superadmin'
  const [route, go] = useRoute()
  const [pending, setPending] = useState(0)

  useEffect(() => {
    if (admin) api.requests().then((r) => setPending(r.length)).catch(() => {})
  }, [admin])

  async function logout() {
    await api.logout()
    onLogout()
  }

  // An app or an external container opens under Apps.
  const page = route[0] === 'containers' ? 'apps' : (route[0] ?? 'apps')
  const tab = (value: string, label: string, count?: number) => (
    <a href={`#/${value}`} className={`chip ${page === value ? 'on' : ''}`} aria-current={page === value ? 'page' : undefined}>
      {label}
      {count ? <span className="count">{count}</span> : null}
    </a>
  )

  return (
    <div className="page">
      <header className="header">
        <Logo size={36} />
        <div className="header-title">
          <div className="brand-name">{instance.name}</div>
          <p className="muted">
            {user.displayName} ({roleLabels[user.role]})
          </p>
        </div>
        <nav className="tabs" aria-label="Sections">
          {tab('apps', 'Apps')}
          {admin && tab('nodes', 'Nodes')}
          {admin && tab('users', 'Utilisateurs', pending)}
          {admin && tab('settings', 'Réglages')}
        </nav>
        <div className="header-actions">
          <ThemeToggle />
          <button type="button" className="btn" onClick={logout}>
            Déconnexion
          </button>
        </div>
      </header>

      {page === 'apps' && <Apps admin={admin} superadmin={user.role === 'superadmin'} route={route.length ? route : ['apps']} go={go} />}
      {page === 'nodes' && admin && <Nodes localSupported={instance.localNodeSupported} />}
      {page === 'users' && admin && <Users me={user} onRequestsChange={setPending} />}
      {page === 'settings' && admin && <Settings superadmin={user.role === 'superadmin'} section={route[1]} onRenamed={onRefresh} />}
    </div>
  )
}

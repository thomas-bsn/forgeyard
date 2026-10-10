import { useEffect, useRef, useState } from 'react'
import { api, type Instance, type User } from './api'
import { Avatar, Logo, useRoute, useTheme } from './ui'
import Settings from './Settings'
import Nodes from './Nodes'
import Apps from './Apps'
import Users from './Users'
import Profile from './Profile'

const roleLabels: Record<User['role'], string> = {
  superadmin: 'superadmin',
  admin: 'admin',
  user: 'utilisateur',
}

const DOCS_URL = 'https://github.com/thomas-bsn/forgeyard/tree/main/docs'

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
      <header className="topbar">
        <a href="#/apps" className="brand-link">
          <Logo size={32} />
          <span className="brand-name">{instance.name}</span>
        </a>
        <nav className="tabs" aria-label="Sections">
          {tab('apps', 'Apps')}
          {admin && tab('nodes', 'Nodes')}
          {admin && tab('users', 'Utilisateurs', pending)}
        </nav>
        <span className="spacer" />
        <UserMenu user={user} admin={admin} pending={pending} onLogout={logout} />
      </header>

      {page === 'apps' && <Apps admin={admin} superadmin={user.role === 'superadmin'} route={route.length ? route : ['apps']} go={go} />}
      {page === 'nodes' && admin && <Nodes localSupported={instance.localNodeSupported} />}
      {page === 'users' && admin && <Users me={user} onRequestsChange={setPending} />}
      {page === 'settings' && admin && <Settings superadmin={user.role === 'superadmin'} section={route[1]} onRenamed={onRefresh} />}
      {page === 'profile' && <Profile user={user} section={route[1]} onChange={onRefresh} />}
    </div>
  )
}

/** The signed-in user's picture, opening a menu: profile, instance settings, theme, docs, sign out. */
function UserMenu({ user, admin, pending, onLogout }: { user: User; admin: boolean; pending: number; onLogout: () => void }) {
  const [open, setOpen] = useState(false)
  const [theme, toggleTheme] = useTheme()
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onClick)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const close = () => setOpen(false)

  return (
    <div className="user-menu" ref={box}>
      <button type="button" className="user-button" aria-haspopup="menu" aria-expanded={open} aria-label="Menu du compte" onClick={() => setOpen(!open)}>
        <Avatar url={user.avatarUrl} name={user.displayName} size={36} />
        {pending > 0 && <span className="user-dot" aria-hidden="true" />}
      </button>
      {open && (
        <div className="menu" role="menu">
          <div className="menu-head">
            <Avatar url={user.avatarUrl} name={user.displayName} size={40} />
            <div>
              <strong>{user.displayName}</strong>
              <span className="muted">
                {roleLabels[user.role]} · {user.method === 'discord' ? 'Discord' : 'identifiant'}
              </span>
            </div>
          </div>
          <a role="menuitem" href="#/profile" onClick={close}>
            Mon profil
          </a>
          {admin && (
            <a role="menuitem" href="#/settings" onClick={close}>
              Réglages de l’instance
            </a>
          )}
          <hr />
          <button type="button" role="menuitem" onClick={toggleTheme}>
            Thème : {theme === 'dark' ? 'sombre' : 'clair'}
            <span className="muted">changer</span>
          </button>
          <a role="menuitem" href={DOCS_URL} target="_blank" rel="noopener noreferrer" onClick={close}>
            Documentation
          </a>
          <hr />
          <button type="button" role="menuitem" className="menu-danger" onClick={onLogout}>
            Déconnexion
          </button>
        </div>
      )}
    </div>
  )
}

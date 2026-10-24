import { useEffect, useRef, useState } from 'react'
import { api, type Instance, type User } from './api'
import { Avatar, Logo, useRoute, useThemePreference } from './ui'
import Settings from './Settings'
import Nodes from './Nodes'
import VisibleNodes from './VisibleNodes'
import Apps from './Apps'
import Users from './Users'
import Profile from './Profile'
import Support from './Support'
import BindRequests from './BindRequests'
import { MemberGrid, MemberPage } from './Members'

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

  // Support: admins see the requests awaiting an answer, members the answers they got.
  const [support, setSupport] = useState(0)
  useEffect(() => {
    const load = () =>
      api.tickets().then(
        (ts) => setSupport(ts.filter((t) => t.status === 'open' && (admin ? t.waiting && t.authorId !== user.id : !t.waiting)).length),
        () => {},
      )
    load()
    const t = setInterval(load, 60000)
    return () => clearInterval(t)
  }, [admin, user.id, route[0], route[1]])

  async function logout() {
    await api.logout()
    onLogout()
  }

  // An app, a project or an external container opens under Apps, a member's profile under Members.
  const aliases: Record<string, string> = { containers: 'apps', projects: 'apps', stacks: 'apps', users: 'members', u: 'members' }
  const page = aliases[route[0]] ?? route[0] ?? 'apps'
  const pageLabels: Record<string, string> = { apps: 'Apps', nodes: 'Nodes', members: 'Membres', support: 'Support', 'bind-requests': 'Demandes', settings: 'Réglages', profile: 'Mon profil' }
  const tab = (value: string, label: string, count?: number) => (
    <a href={`#/${value}`} className={`chip ${page === value ? 'on' : ''}`} aria-current={page === value ? 'page' : undefined}>
      {label}
      {count ? <span className="count">{count}</span> : null}
    </a>
  )

  return (
    <div className="dashboard-shell">
      <header className="topbar">
        <a href="#/apps" className="brand-link">
          <Logo size={32} src={instance.iconUrl} />
          <span className="brand-name">{instance.name}</span>
        </a>
        <nav className="tabs" aria-label="Sections">
          {tab('apps', 'Apps')}
          {tab('nodes', 'Nodes')}
          {tab('members', 'Membres', admin ? pending : 0)}
          {tab('support', 'Support', support)}
          {tab('bind-requests', 'Mes demandes')}
        </nav>
        <span className="spacer" />
        <UserMenu user={user} admin={admin} pending={pending} onLogout={logout} />
      </header>

      <div className="dashboard-content">
        <div className="workspace-breadcrumb"><a href="#/apps">Forgeyard</a><span aria-hidden="true">›</span><a href={`#/${page}`}>{pageLabels[page] ?? 'Apps'}</a>{route[1] && <><span aria-hidden="true">›</span><span>{route[1]}</span></>}</div>
        {page === 'apps' && <Apps admin={admin} superadmin={user.role === 'superadmin'} userId={user.id} route={route.length ? route : ['apps']} go={go} />}
        {page === 'nodes' && (admin ? <Nodes localSupported={instance.localNodeSupported} /> : <VisibleNodes />)}
        {route[0] === 'u' && route[1] ? (
          <MemberPage id={Number(route[1])} me={user} />
        ) : (
          page === 'members' && (admin ? <Users me={user} onRequestsChange={setPending} /> : <MemberGrid />)
        )}
        {page === 'settings' && admin && <Settings superadmin={user.role === 'superadmin'} section={route[1]} onRenamed={onRefresh} />}
        {page === 'bind-requests' && <BindRequests superadmin={user.role === 'superadmin'} route={route} go={go} />}
        {page === 'support' && <Support me={user} admin={admin} route={route} go={go} />}
        {page === 'profile' && <Profile user={user} section={route[1]} onChange={onRefresh} />}
      </div>
    </div>
  )
}

/** The signed-in user's picture, opening a menu: profile, instance settings, theme, docs, sign out. */
function UserMenu({ user, admin, pending, onLogout }: { user: User; admin: boolean; pending: number; onLogout: () => void }) {
  const [open, setOpen] = useState(false)
  const [theme, setTheme] = useThemePreference()
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
      <button type="button" className="user-button" aria-haspopup="menu" aria-expanded={open} aria-label={`Menu du profil de ${user.displayName}`} onClick={() => setOpen(!open)}>
        <Avatar url={user.avatarUrl} name={user.displayName} size={36} />
        <span className="user-name">{user.displayName}</span>
        <svg className="user-chevron" width="14" height="14" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m5 7.5 5 5 5-5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg>
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
          <div className="menu-theme" role="group" aria-label="Apparence">
            <span className="menu-theme-label">Apparence</span>
            <div className="menu-theme-options">
              {([
                ['system', 'Système'],
                ['light', 'Clair'],
                ['dark', 'Sombre'],
              ] as const).map(([value, label]) => (
                <button key={value} type="button" role="menuitemradio" aria-label={`Apparence : ${label}`} title={label} aria-checked={theme === value} className={theme === value ? 'selected' : ''} onClick={() => { setTheme(value); close() }}>
                  <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    {value === 'system' ? (
                      <><rect x="3" y="4" width="18" height="13" rx="2" /><path d="M8 21h8M12 17v4" /></>
                    ) : value === 'light' ? (
                      <><circle cx="12" cy="12" r="4" /><path d="M12 2v2m0 16v2M4.93 4.93l1.42 1.42m11.3 11.3 1.42 1.42M2 12h2m16 0h2M4.93 19.07l1.42-1.42m11.3-11.3 1.42-1.42" /></>
                    ) : (
                      <path d="M20.8 14.2A8.8 8.8 0 0 1 9.8 3.2 8.8 8.8 0 1 0 20.8 14.2Z" />
                    )}
                  </svg>
                </button>
              ))}
            </div>
          </div>
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

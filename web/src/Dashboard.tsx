import { api, type Instance, type User } from './api'

const roleLabels: Record<User['role'], string> = {
  superadmin: 'Superadmin',
  admin: 'Admin',
  user: 'Utilisateur',
}

export default function Dashboard({
  instance,
  user,
  onLogout,
}: {
  instance: Instance
  user: User
  onLogout: () => void
}) {
  async function logout() {
    await api.logout()
    onLogout()
  }

  return (
    <>
      <header className="topbar">
        <strong>{instance.name}</strong>
        <span className="spacer" />
        <span className="muted">
          {user.displayName} · {roleLabels[user.role]}
        </span>
        <button className="secondary" onClick={logout}>
          Déconnexion
        </button>
      </header>
      <main className="page">
        <h1>Apps</h1>
        <div className="empty">
          <p>Aucune app pour le moment.</p>
          <p className="muted">Le déploiement arrive dans la prochaine étape de la v0.1.</p>
        </div>
      </main>
    </>
  )
}

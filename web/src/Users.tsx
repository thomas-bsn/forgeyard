import { useEffect, useState } from 'react'
import { api, errorMessage, type Account, type AccountRequest, type App, type User } from './api'
import { Modal } from './ui'

const roleLabels: Record<Account['role'], string> = { superadmin: 'superadmin', admin: 'admin', user: 'utilisateur' }

/** Accounts and the requests waiting for an answer. */
export default function Users({ me, onRequestsChange }: { me: User; onRequestsChange: (n: number) => void }) {
  const [users, setUsers] = useState<Account[] | null>(null)
  const [requests, setRequests] = useState<AccountRequest[]>([])
  const [apps, setApps] = useState<App[]>([])
  const [managing, setManaging] = useState<number | null>(null)
  const [error, setError] = useState('')

  async function load() {
    try {
      const [u, r, a] = await Promise.all([api.users(), api.requests(), api.apps()])
      setUsers(u)
      setRequests(r)
      setApps(a)
      onRequestsChange(r.length)
      setError('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  useEffect(() => {
    load()
  }, [])

  const target = users?.find((u) => u.id === managing)

  return (
    <div className="section">
      <div className="toolbar">
        <h1 className="toolbar-title">
          Utilisateurs{' '}
          <span className="muted">
            {users?.length ?? 0} compte{(users?.length ?? 0) > 1 ? 's' : ''}
          </span>
        </h1>
      </div>
      {error && <p className="error">{error}</p>}

      {requests.length > 0 && (
        <section className="panel requests-panel">
          <h2>
            Demandes en attente <span className="text-warn">{requests.length}</span>
          </h2>
          {requests.map((r) => (
            <RequestRow key={r.id} request={r} onDone={load} />
          ))}
        </section>
      )}

      {users && (
        <div className="table-box">
          <table className="data-table">
            <thead>
              <tr>
                <th>Compte</th>
                <th>Connexion</th>
                <th>Rôle</th>
                <th>Apps</th>
                <th>État</th>
                <th>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const editable = u.role !== 'superadmin' && u.id !== me.id
                return (
                  <tr key={u.id}>
                    <td>
                      <span className="user-cell">
                        <span className="avatar avatar-sm" aria-hidden="true">
                          {u.displayName.charAt(0).toUpperCase()}
                        </span>
                        <strong>{u.displayName}</strong>
                      </span>
                    </td>
                    <td>{u.method === 'discord' ? 'Discord' : 'Mot de passe'}</td>
                    <td>
                      <span className={`badge ${u.role === 'superadmin' ? 'badge-accent' : ''}`}>{roleLabels[u.role]}</span>
                    </td>
                    <td>{u.appCount}</td>
                    <td>{u.disabled ? <span className="muted">Désactivé</span> : u.appsSuspended ? <span className="text-warn">Apps suspendues</span> : <span className="text-up">Actif</span>}</td>
                    <td className="cell-actions">
                      {editable ? (
                        <button type="button" className="btn btn-small" onClick={() => setManaging(u.id)}>
                          Gérer…
                        </button>
                      ) : (
                        <span className="muted">{u.id === me.id ? 'vous' : ''}</span>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {target && (
        <ManageUser
          user={target}
          apps={apps.filter((a) => a.ownerId === target.id)}
          onClose={() => setManaging(null)}
          onChanged={load}
        />
      )}
    </div>
  )
}

function RequestRow({ request, onDone }: { request: AccountRequest; onDone: () => void }) {
  const [role, setRole] = useState<'user' | 'admin'>('user')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function run(action: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await action()
      onDone()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <div className="request-row">
      {request.avatarUrl ? (
        <img className="avatar avatar-sm" src={request.avatarUrl} alt="" />
      ) : (
        <span className="avatar avatar-sm" aria-hidden="true">
          {request.displayName.charAt(0).toUpperCase()}
        </span>
      )}
      <div className="list-main">
        <strong>{request.displayName}</strong>
        <p className="muted">
          @{request.username}
          {request.email ? ` · ${request.email}` : ''} · {new Date(request.createdAt * 1000).toLocaleString('fr-FR')}
        </p>
        {error && <p className="error">{error}</p>}
      </div>
      <div className="row-actions">
        <select value={role} onChange={(e) => setRole(e.target.value as 'user' | 'admin')} aria-label="Rôle" disabled={busy}>
          <option value="user">Utilisateur</option>
          <option value="admin">Admin</option>
        </select>
        <button type="button" className="btn btn-success" disabled={busy} onClick={() => run(() => api.acceptRequest(request.id, role))}>
          Accepter
        </button>
        <button
          type="button"
          className="btn"
          disabled={busy}
          onClick={() => {
            const reason = window.prompt('Motif du refus (facultatif) :')
            if (reason !== null) run(() => api.refuseRequest(request.id, reason))
          }}
        >
          Refuser
        </button>
      </div>
    </div>
  )
}

function ManageUser({ user, apps, onClose, onChanged }: { user: Account; apps: App[]; onClose: () => void; onChanged: () => void }) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function run(action: () => Promise<unknown>, close = false) {
    setBusy(true)
    setError('')
    try {
      await action()
      onChanged()
      if (close) onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={user.displayName}
      subtitle={`${user.method === 'discord' ? 'Discord' : 'Identifiant et mot de passe'} · inscrit le ${new Date(user.createdAt * 1000).toLocaleDateString('fr-FR')}`}
      onClose={onClose}
      footer={
        <button type="button" className="btn btn-primary" onClick={onClose}>
          Fermer
        </button>
      }
    >
      <label className="field">
        <span>Rôle</span>
        <select value={user.role} disabled={busy} onChange={(e) => run(() => api.updateUser(user.id, { role: e.target.value as 'user' | 'admin' }))}>
          <option value="user">Utilisateur : ses propres apps</option>
          <option value="admin">Admin : toutes les apps, les nodes et les comptes</option>
        </select>
      </label>

      <div className="field">
        <span>Ses apps</span>
        {apps.length === 0 ? (
          <p className="muted">Aucune app.</p>
        ) : (
          <div className="mini-list">
            {apps.map((a) => (
              <a key={a.id} href={`#/apps/${a.id}`} className="mini-list-row" onClick={onClose}>
                <span className={`dot ${a.state === 'running' ? 'dot-up' : ['exited', 'restarting', 'error'].includes(a.state) ? 'dot-down' : ''}`} />
                <strong>{a.name}</strong>
                <span className="muted">
                  {a.nodeName} · {a.image}
                </span>
              </a>
            ))}
          </div>
        )}
      </div>

      <div className="danger-zone">
        <div className="danger-row">
          <div>
            <strong>{user.appsSuspended ? 'Apps suspendues' : 'Suspendre ses apps'}</strong>
            <small className="muted">
              {user.appsSuspended
                ? 'Ses apps restent arrêtées et ne peuvent pas être relancées. Réactiver ne les redémarre pas.'
                : 'Arrête toutes ses apps. Il peut encore se connecter mais pas les relancer.'}
            </small>
          </div>
          <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.updateUser(user.id, { appsSuspended: !user.appsSuspended }))}>
            {user.appsSuspended ? 'Réactiver' : 'Suspendre'}
          </button>
        </div>
        <div className="danger-row">
          <div>
            <strong>{user.disabled ? 'Compte désactivé' : 'Désactiver le compte'}</strong>
            <small className="muted">{user.disabled ? 'Il ne peut plus se connecter.' : 'Le déconnecte et bloque ses connexions. Réversible.'}</small>
          </div>
          <button type="button" className="btn" disabled={busy} onClick={() => run(() => api.updateUser(user.id, { disabled: !user.disabled }))}>
            {user.disabled ? 'Réactiver' : 'Désactiver'}
          </button>
        </div>
        <div className="danger-row">
          <div>
            <strong className="text-down">Supprimer le compte</strong>
            <small className="muted">Supprime aussi ses apps, leurs conteneurs et leurs DNS.</small>
          </div>
          <button
            type="button"
            className="btn btn-danger"
            disabled={busy}
            onClick={() => {
              if (window.confirm(`Supprimer le compte « ${user.displayName} » et ses ${apps.length} app(s) ? C’est définitif.`)) {
                run(() => api.deleteUser(user.id), true)
              }
            }}
          >
            Supprimer…
          </button>
        </div>
      </div>
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

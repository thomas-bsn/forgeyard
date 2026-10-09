import { useEffect, useState } from 'react'
import { api, errorMessage, type AccountRequest } from './api'

export default function Requests({ onCountChange }: { onCountChange: (n: number) => void }) {
  const [requests, setRequests] = useState<AccountRequest[] | null>(null)
  const [error, setError] = useState('')

  async function load() {
    try {
      const r = await api.requests()
      setRequests(r)
      onCountChange(r.length)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  useEffect(() => {
    load()
  }, [])

  if (error) return <p className="error">{error}</p>
  if (!requests) return null
  if (requests.length === 0) {
    return (
      <div className="empty-state">
        <strong>Aucune demande en attente</strong>
        <span>Quand un compte Discord inconnu se connecte, sa demande apparaît ici.</span>
      </div>
    )
  }
  return (
    <div className="list">
      {requests.map((r) => (
        <RequestRow key={r.id} request={r} onDone={load} />
      ))}
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
    <div className="list-row">
      {request.avatarUrl ? (
        <img className="avatar" src={request.avatarUrl} alt="" />
      ) : (
        <span className="avatar" aria-hidden="true">
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
        <button type="button" className="btn btn-primary" disabled={busy} onClick={() => run(() => api.acceptRequest(request.id, role))}>
          Accepter
        </button>
        <button
          type="button"
          className="btn btn-danger"
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

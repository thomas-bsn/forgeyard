import { useEffect, useState } from 'react'
import { AccessEntry, AccessList, AccessRole, Member, api, errorMessage } from './api'
import { Avatar } from './ui'

export const roleLabel: Record<AccessEntry['role'], string> = { owner: 'Propriétaire', contributor: 'Contributeur', viewer: 'Lecteur' }

/** The small mention shown on an app or a project that someone shared with the signed-in user. */
export function RoleBadge({ role, via }: { role?: AccessRole; via?: 'project' }) {
  if (!role) return null
  return (
    <span className="badge" title={via === 'project' ? 'Ce rôle vient de son projet' : undefined}>
      {roleLabel[role]}
      {via === 'project' ? ' · via le projet' : ''}
    </span>
  )
}

/**
 * Who may use an app or a project. Everyone with access sees the list; only the owner (and the admins) adds,
 * changes or removes people, or hands the resource over.
 */
export default function Access({ kind, id, name, owner, onTransferred }: { kind: 'apps' | 'stacks'; id: number; name: string; owner: boolean; onTransferred: () => void }) {
  const [list, setList] = useState<AccessList | null>(null)
  const [people, setPeople] = useState<Member[]>([])
  const [pick, setPick] = useState('')
  const [role, setRole] = useState<AccessRole>('contributor')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let live = true
    api.access(kind, id).then((l) => live && setList(l), (err) => live && setError(errorMessage(err)))
    if (owner) api.members().then((m) => live && setPeople(m), () => {})
    return () => {
      live = false
    }
  }, [kind, id, owner])

  async function run(action: () => Promise<AccessList | unknown>, done?: () => void) {
    setBusy(true)
    setError('')
    try {
      const result = await action()
      if (result && typeof result === 'object' && 'members' in result) setList(result as AccessList)
      done?.()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (!list) return error ? <p className="error">{error}</p> : <div className="empty-state">Chargement…</div>

  const taken = new Set([list.owner.userId, ...list.members.filter((m) => !m.via).map((m) => m.userId)])
  const candidates = people.filter((p) => !taken.has(p.id))

  return (
    <div className="access">
      <p className="muted">
        {kind === 'apps' ? 'Cette app' : 'Ce projet'} est à {list.owner.displayName}. Un <strong>lecteur</strong> voit son état et ses logs. Un{' '}
        <strong>contributeur</strong> peut aussi la déployer et modifier sa configuration et ses variables, sans pouvoir les lire, la supprimer ni ouvrir de
        terminal.
      </p>
      <ul className="access-list">
        {[list.owner, ...list.members].map((m) => (
          <li key={`${m.userId}-${m.via ?? ''}`} className="setting-row">
            <span className="header-title">
              <Avatar url={m.avatarUrl} name={m.displayName} size={28} />
              <a href={`#/u/${m.userId}`}>{m.displayName}</a>
              {m.via && <span className="muted"> · via le projet</span>}
            </span>
            {owner && m.role !== 'owner' && !m.via ? (
              <>
                <select
                  aria-label={`Rôle de ${m.displayName}`}
                  value={m.role}
                  disabled={busy}
                  onChange={(e) => run(() => api.setAccess(kind, id, m.userId, e.target.value as AccessRole))}
                >
                  <option value="viewer">Lecteur</option>
                  <option value="contributor">Contributeur</option>
                </select>
                <button type="button" className="btn btn-small" disabled={busy} onClick={() => run(() => api.removeAccess(kind, id, m.userId))}>
                  Retirer
                </button>
              </>
            ) : (
              <span className="badge">{roleLabel[m.role]}</span>
            )}
          </li>
        ))}
      </ul>

      {owner && (
        <form
          className="access-add"
          onSubmit={(e) => {
            e.preventDefault()
            if (pick) run(() => api.setAccess(kind, id, Number(pick), role), () => setPick(''))
          }}
        >
          <select aria-label="Membre à ajouter" value={pick} onChange={(e) => setPick(e.target.value)}>
            <option value="">Ajouter quelqu’un…</option>
            {candidates.map((p) => (
              <option key={p.id} value={p.id}>
                {p.displayName}
              </option>
            ))}
          </select>
          <select aria-label="Rôle" value={role} onChange={(e) => setRole(e.target.value as AccessRole)}>
            <option value="viewer">Lecteur</option>
            <option value="contributor">Contributeur</option>
          </select>
          <button type="submit" className="btn btn-small" disabled={busy || !pick}>
            Ajouter
          </button>
        </form>
      )}
      {error && <p className="error">{error}</p>}

      {owner && (
        <div className="access-transfer">
          <strong>Transférer la propriété</strong>
          <span className="muted">
            {name} (et ses services, pour un projet) passe à la personne choisie : vous n’y aurez plus accès, sauf si elle vous donne un rôle.
          </span>
          <TransferTo candidates={people.filter((p) => p.id !== list.owner.userId)} disabled={busy} onPick={(p) => {
            if (window.confirm(`Transférer « ${name} » à ${p.displayName} ? Vous perdrez l’accès à ${kind === 'apps' ? 'cette app' : 'ce projet'}.`)) {
              run(() => api.transfer(kind, id, p.id), onTransferred)
            }
          }} />
        </div>
      )}
    </div>
  )
}

function TransferTo({ candidates, disabled, onPick }: { candidates: Member[]; disabled: boolean; onPick: (m: Member) => void }) {
  const [pick, setPick] = useState('')
  return (
    <span className="access-add">
      <select aria-label="Nouveau propriétaire" value={pick} onChange={(e) => setPick(e.target.value)}>
        <option value="">Choisir…</option>
        {candidates.map((p) => (
          <option key={p.id} value={p.id}>
            {p.displayName}
          </option>
        ))}
      </select>
      <button
        type="button"
        className="btn btn-small danger"
        disabled={disabled || !pick}
        onClick={() => {
          const p = candidates.find((c) => String(c.id) === pick)
          if (p) onPick(p)
        }}
      >
        Transférer…
      </button>
    </span>
  )
}

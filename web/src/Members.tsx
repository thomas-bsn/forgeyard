import { useEffect, useState } from 'react'
import { api, errorMessage, type Member, type MemberProfile, type User } from './api'
import { Banner } from './Profile'
import { AppLogo, Avatar } from './ui'

const roleLabels: Record<Member['role'], string> = { superadmin: 'superadmin', admin: 'admin', user: 'membre' }

/** Everyone signed up on the instance, to find people and open their profile. */
export function MemberGrid() {
  const [members, setMembers] = useState<Member[] | null>(null)
  const [query, setQuery] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    api.members().then(setMembers, (err) => setError(errorMessage(err)))
  }, [])

  const q = query.trim().toLowerCase()
  const shown = (members ?? []).filter((m) => !q || m.displayName.toLowerCase().includes(q) || m.bio.toLowerCase().includes(q))

  return (
    <div className="section">
      <div className="toolbar">
        <h1 className="toolbar-title">
          Membres <span className="muted">{members?.length ?? 0}</span>
        </h1>
        <label className="search">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="m20 20-3.5-3.5" />
          </svg>
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Nom, description…" aria-label="Rechercher un membre" />
        </label>
      </div>
      {error && <p className="error">{error}</p>}
      <div className="card-grid">
        {shown.map((m) => (
          <a key={m.id} href={`#/u/${m.id}`} className="member-card">
            <Avatar url={m.avatarUrl} name={m.displayName} size={48} />
            <span className="member-card-main">
              <strong>{m.displayName}</strong>
              <span className="muted">
                {roleLabels[m.role]} · {m.publicApps} app{m.publicApps > 1 ? 's' : ''}
              </span>
              {m.bio && <span className="member-bio">{m.bio}</span>}
            </span>
          </a>
        ))}
      </div>
    </div>
  )
}

const stateText: Record<string, [string, string]> = {
  running: ['En ligne', 'dot-up'],
  stopped: ['Arrêtée', ''],
  exited: ['Plantée', 'dot-down'],
  restarting: ['Redémarre', 'dot-down'],
  error: ['Erreur', 'dot-down'],
  'node-offline': ['Hors ligne', 'dot-down'],
}

/** A member's public profile: who they are, the apps they show, and how to reach them. */
export function MemberPage({ id, me }: { id: number; me: User }) {
  const [p, setP] = useState<MemberProfile | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    setP(null)
    api.member(id).then(setP, (err) => setError(errorMessage(err)))
  }, [id])

  if (error) return <div className="empty-state">{error}</div>
  if (!p) return null
  const self = p.id === me.id
  const since = new Date(p.createdAt * 1000).toLocaleDateString('fr-FR', { month: 'long', year: 'numeric' })

  return (
    <div className="member-page">
      <section className="panel member-head">
        <Banner url={p.bannerUrl} color={p.accentColor} height={180} />
        <div className="member-head-body">
          <Avatar url={p.avatarUrl} name={p.displayName} size={104} />
          <div className="member-head-main">
            <h1>{p.displayName}</h1>
            <span className="muted">
              {roleLabels[p.role]} · membre depuis {since}
            </span>
          </div>
          <div className="row-actions">
            {self ? (
              <a className="btn" href="#/profile">
                Modifier mon profil
              </a>
            ) : (
              <>
                {p.discordId && (
                  <a className="btn btn-discord" href={`https://discord.com/users/${p.discordId}`} target="_blank" rel="noopener noreferrer">
                    Écrire sur Discord
                  </a>
                )}
                {p.email && (
                  <a className="btn" href={`mailto:${p.email}`}>
                    Envoyer un email
                  </a>
                )}
              </>
            )}
          </div>
        </div>
        {p.bio && <p className="member-bio-full">{p.bio}</p>}
      </section>

      <div className="member-columns">
        <section className="panel">
          <h2>Apps</h2>
          {!p.showApps ? (
            <p className="muted">{self ? 'Vous avez choisi de ne pas afficher vos apps.' : `${p.displayName} n’affiche pas ses apps.`}</p>
          ) : p.apps.length === 0 ? (
            <p className="muted">Aucune app publique.</p>
          ) : (
            <div className="mini-list">
              {p.apps.map((a) => {
                const [label, dot] = stateText[a.state] ?? ['En attente', '']
                return (
                  <div key={a.id} className="mini-list-row">
                    <AppLogo url={a.logo.url} color={a.logo.color} name={a.name} size={32} />
                    <span className={`dot ${dot}`} title={label} />
                    <strong>{a.name}</strong>
                    {a.url ? (
                      <a className="member-app-url" href={a.url} target="_blank" rel="noopener noreferrer">
                        {a.url.replace(/^https?:\/\//, '')} ↗
                      </a>
                    ) : (
                      <span className="muted member-app-url">pas d’adresse web</span>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </section>
        {p.showApps && (
          <section className="panel">
            <h2>Activité récente</h2>
            {p.activity.length === 0 ? (
              <p className="muted">Rien pour le moment.</p>
            ) : (
              <ol className="events plain">
                {p.activity.map((e, i) => (
                  <li key={i}>
                    <time>{new Date(e.at * 1000).toLocaleDateString('fr-FR', { day: '2-digit', month: 'short' })}</time>
                    <span className={`dot ${{ info: '', success: 'dot-up', warning: 'dot-warn', error: 'dot-down' }[e.kind]}`} />
                    <span>
                      <b>{e.app}</b> · {e.message}
                    </span>
                  </li>
                ))}
              </ol>
            )}
          </section>
        )}
      </div>
    </div>
  )
}

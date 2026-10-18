import { useEffect, useState } from 'react'
import { api, errorMessage, type Member, type MemberApp, type MemberProfile, type MemberProject, type User } from './api'
import { Banner } from './Profile'
import { kindLabels, ProjectLogo, projectStates } from './Projects'
import { AppLogo, Avatar } from './ui'

const roleLabels: Record<Member['role'], string> = { superadmin: 'superadmin', admin: 'admin', user: 'membre' }

/** How many apps and projects a member shows: "3 apps · 1 projet". */
function shownCounts(apps: number, projects: number): string {
  const a = `${apps} app${apps > 1 ? 's' : ''}`
  return projects === 0 ? a : `${a} · ${projects} projet${projects > 1 ? 's' : ''}`
}

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
                {roleLabels[m.role]} · {shownCounts(m.publicApps, m.publicProjects)}
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
  finished: ['Terminée', ''],
  exited: ['Plantée', 'dot-down'],
  restarting: ['Redémarre', 'dot-down'],
  error: ['Erreur', 'dot-down'],
  'node-offline': ['Hors ligne', 'dot-down'],
}

/** One line of a profile's app list: its logo, state, name and address (or what kind it is). */
function MemberAppRow({ app }: { app: MemberApp }) {
  const [label, dot] = stateText[app.state] ?? ['En attente', '']
  return (
    <div className="mini-list-row">
      <AppLogo url={app.logo.url} color={app.logo.color} name={app.name} size={32} />
      <span className={`dot ${dot}`} title={label} />
      <strong>{app.service || app.name}</strong>
      {app.url ? (
        <a className="member-app-url" href={app.url} target="_blank" rel="noopener noreferrer">
          {app.url.replace(/^https?:\/\//, '')} ↗
        </a>
      ) : (
        <span className="muted member-app-url">{app.kind ? (kindLabels[app.kind] ?? app.kind) : 'pas d’adresse web'}</span>
      )}
    </div>
  )
}

/** A project on a profile: one line, opening the project with its apps. */
function MemberProjectRow({ project }: { project: MemberProject }) {
  const st = projectStates[project.state] ?? { label: project.state, tone: '' }
  const dot = st.tone === 'up' ? 'dot-up' : st.tone === 'warn' ? 'dot-warn' : st.tone === 'down' ? 'dot-down' : ''
  const apps = project.services.length
  return (
    <a className="mini-list-row" href={`#/projects/${project.id}`}>
      <ProjectLogo project={project} size={32} />
      <span className={`dot ${dot}`} title={st.label} />
      <strong>{project.name}</strong>
      <span className="member-project-label">
        {apps} app{apps > 1 ? 's' : ''} · {st.label}
      </span>
      <span className={project.url ? 'member-app-url' : 'muted member-app-url'}>
        {project.url ? `${project.url.replace(/^https?:\/\//, '')} ↗` : 'aucun service exposé'}
      </span>
      <span className="row-chevron" aria-hidden="true">
        ›
      </span>
    </a>
  )
}

/** A member's public profile: who they are, the apps and projects they show, and how to reach them. */
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
        {!p.showApps ? (
          <section className="panel">
            <h2>Apps et projets</h2>
            <p className="muted">
              {self ? 'Vous avez choisi de ne pas afficher vos apps ni vos projets.' : `${p.displayName} n’affiche ni ses apps ni ses projets.`}
            </p>
          </section>
        ) : (
          <>
            <section className="panel">
              <h2>
                Apps <span className="muted">{p.apps.length}</span>
              </h2>
              {p.apps.length === 0 ? (
                <p className="muted">Aucune app seule.</p>
              ) : (
                <div className="mini-list">
                  {p.apps.map((a) => (
                    <MemberAppRow key={a.id} app={a} />
                  ))}
                </div>
              )}
            </section>
            <section className="panel">
              <h2>
                Projets <span className="muted">{p.projects.length}</span>
              </h2>
              {p.projects.length === 0 ? (
                <p className="muted">Aucun projet.</p>
              ) : (
                <div className="mini-list">
                  {p.projects.map((proj) => (
                    <MemberProjectRow key={proj.id} project={proj} />
                  ))}
                </div>
              )}
            </section>
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
          </>
        )}
      </div>
    </div>
  )
}

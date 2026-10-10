import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { api, errorMessage, type App, type Ticket, type TicketDetail, type TicketKind, type User } from './api'
import { Avatar, Modal, since } from './ui'

const POLL_MS = 15000

const kinds: { key: TicketKind; label: string; text: string; icon: ReactNode }[] = [
  {
    key: 'general',
    label: 'Général',
    text: 'Une question, une idée',
    icon: (
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12Z" />
      </svg>
    ),
  },
  {
    key: 'app',
    label: 'Une app',
    text: 'Elle plante, ne répond pas…',
    icon: (
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <rect x="3" y="3" width="7" height="7" rx="2" />
        <rect x="14" y="3" width="7" height="7" rx="2" />
        <rect x="3" y="14" width="7" height="7" rx="2" />
        <rect x="14" y="14" width="7" height="7" rx="2" />
      </svg>
    ),
  },
  {
    key: 'infra',
    label: 'Infra',
    text: 'Un node, le réseau, le DNS',
    icon: (
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <rect x="3" y="4" width="18" height="7" rx="2" />
        <rect x="3" y="13" width="18" height="7" rx="2" />
        <path d="M7 7.5h.01M7 16.5h.01" />
      </svg>
    ),
  },
]

const kindOf = (k: TicketKind) => kinds.find((x) => x.key === k)!

type Filter = 'open' | 'closed' | 'all'

/** Help requests: members write to the admins, about anything, one of their apps or the infrastructure. */
export default function Support({ me, admin, route, go }: { me: User; admin: boolean; route: string[]; go: (path: string) => void }) {
  const [tickets, setTickets] = useState<Ticket[] | null>(null)
  const [filter, setFilter] = useState<Filter>('open')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const selected = Number(route[1]) || 0

  const load = () => api.tickets().then(setTickets, (err) => setError(errorMessage(err)))
  useEffect(() => {
    load()
    const t = setInterval(load, POLL_MS)
    return () => clearInterval(t)
  }, [])

  const shown = (tickets ?? []).filter((t) => filter === 'all' || t.status === filter)
  const count = (f: Filter) => (tickets ?? []).filter((t) => f === 'all' || t.status === f).length

  return (
    <div className="support">
      <aside className="panel support-list">
        <div className="support-list-head">
          <h1>Support</h1>
          <button type="button" className="btn btn-primary btn-small" onClick={() => setCreating(true)}>
            + Nouvelle demande
          </button>
        </div>
        <div className="segmented segmented-small" role="tablist" aria-label="Filtrer les demandes">
          {(['open', 'closed', 'all'] as Filter[]).map((f) => (
            <button key={f} type="button" role="tab" aria-selected={filter === f} className={filter === f ? 'on' : ''} onClick={() => setFilter(f)}>
              {{ open: 'Ouvertes', closed: 'Fermées', all: 'Toutes' }[f]} {count(f)}
            </button>
          ))}
        </div>
        {error && <p className="error">{error}</p>}
        {tickets && shown.length === 0 && (
          <p className="muted support-empty">{filter === 'open' ? 'Aucune demande en cours.' : 'Rien ici.'}</p>
        )}
        <nav className="ticket-list" aria-label="Demandes">
          {shown.map((t) => {
            const k = kindOf(t.kind)
            // Admins see what awaits an answer; members see what got one.
            const flag = t.status === 'open' && (admin ? t.waiting && t.authorId !== me.id : !t.waiting)
            return (
              <a key={t.id} href={`#/support/${t.id}`} className={`ticket-item ${t.id === selected ? 'on' : ''}`} aria-current={t.id === selected ? 'page' : undefined}>
                <span className={`ticket-kind kind-${t.kind}`} aria-hidden="true">
                  {k.icon}
                </span>
                <span className="ticket-item-text">
                  <strong>{t.subject}</strong>
                  <span className="muted">
                    {admin ? `${t.authorName} · ` : ''}
                    {t.appName ? `${t.appName} · ` : k.label + ' · '}
                    {since(t.updatedAt)}
                  </span>
                </span>
                {flag && <span className="ticket-flag" title={admin ? 'À répondre' : 'Réponse reçue'} />}
              </a>
            )
          })}
        </nav>
      </aside>

      <main className="support-main">
        {selected ? (
          <Thread
            key={selected}
            id={selected}
            me={me}
            admin={admin}
            onChange={load}
          />
        ) : (
          <div className="empty-state support-welcome">
            <strong>Besoin d’aide ?</strong>
            <span>
              Écris aux admins de l’instance : une question, une app qui ne marche pas, un souci de node. Ils répondent ici
              {admin ? '' : ', et sur ton webhook Discord si tu en as un (Mon profil › Notifications)'}.
            </span>
            <button type="button" className="btn btn-primary" onClick={() => setCreating(true)}>
              Nouvelle demande
            </button>
          </div>
        )}
      </main>

      {creating && (
        <NewTicket
          onClose={() => setCreating(false)}
          onCreated={(t) => {
            setCreating(false)
            setFilter('open')
            load()
            go(`support/${t.id}`)
          }}
        />
      )}
    </div>
  )
}

function Thread({ id, me, admin, onChange }: { id: number; me: User; admin: boolean; onChange: () => void }) {
  const [ticket, setTicket] = useState<TicketDetail | null>(null)
  const [reply, setReply] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const load = () => api.ticket(id).then(setTicket, (err) => setError(errorMessage(err)))
    load()
    const t = setInterval(load, POLL_MS)
    return () => clearInterval(t)
  }, [id])

  async function run(action: () => Promise<TicketDetail>) {
    setBusy(true)
    setError('')
    try {
      setTicket(await action())
      onChange()
      return true
    } catch (err) {
      setError(errorMessage(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  async function send(e: FormEvent) {
    e.preventDefault()
    if (await run(() => api.replyTicket(id, reply))) setReply('')
  }

  if (!ticket) return error ? <p className="error">{error}</p> : <div className="empty-state">Chargement…</div>
  const k = kindOf(ticket.kind)
  const closed = ticket.status === 'closed'

  return (
    <section className="panel thread">
      <header className="thread-head">
        <span className={`ticket-kind kind-${ticket.kind}`} aria-hidden="true">
          {k.icon}
        </span>
        <div className="header-title">
          <h2>{ticket.subject}</h2>
          <span className="muted">
            {k.label}
            {ticket.appName && (
              <>
                {' · '}
                {ticket.appId ? <a href={`#/apps/${ticket.appId}`}>{ticket.appName}</a> : ticket.appName}
              </>
            )}
            {admin && ` · par ${ticket.authorName}`} · ouverte {since(ticket.createdAt)}
          </span>
        </div>
        <span className={`badge ${closed ? '' : 'badge-accent'}`}>{closed ? 'Fermée' : 'Ouverte'}</span>
        <button type="button" className="btn btn-small" disabled={busy} onClick={() => run(() => api.setTicketStatus(id, closed ? 'open' : 'closed'))}>
          {closed ? 'Rouvrir' : 'Fermer'}
        </button>
      </header>

      <ol className="messages">
        {ticket.thread.map((m) => {
          const mine = m.authorId === me.id
          return (
            <li key={m.id} className={`message ${mine ? 'mine' : ''} ${m.staff ? 'staff' : ''}`}>
              <Avatar url={m.avatarUrl} name={m.authorName} size={32} />
              <div className="bubble">
                <span className="bubble-head">
                  <strong>{mine ? 'Toi' : m.authorName}</strong>
                  {m.staff && <span className="staff-tag">admin</span>}
                  <span className="muted">{new Date(m.createdAt * 1000).toLocaleString('fr-FR', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })}</span>
                </span>
                <p>{m.body}</p>
              </div>
            </li>
          )
        })}
      </ol>

      <form className="reply" onSubmit={send}>
        <textarea
          value={reply}
          onChange={(e) => setReply(e.target.value)}
          placeholder={closed ? 'Écrire rouvre la demande…' : 'Ta réponse…'}
          rows={3}
          maxLength={5000}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) send(e)
          }}
        />
        <div className="reply-foot">
          {error ? <p className="error">{error}</p> : <span className="muted">⌘/Ctrl + Entrée pour envoyer</span>}
          <button type="submit" className="btn btn-primary" disabled={busy || !reply.trim()}>
            Envoyer
          </button>
        </div>
      </form>
    </section>
  )
}

function NewTicket({ onClose, onCreated }: { onClose: () => void; onCreated: (t: TicketDetail) => void }) {
  const [kind, setKind] = useState<TicketKind>('general')
  const [apps, setApps] = useState<App[]>([])
  const [appId, setAppId] = useState(0)
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.apps().then((a) => {
      setApps(a)
      setAppId(a[0]?.id ?? 0)
    }, () => {})
  }, [])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      onCreated(await api.createTicket({ kind, appId: kind === 'app' ? appId : undefined, subject, body }))
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <Modal
      title="Nouvelle demande"
      subtitle="Les admins de l’instance la reçoivent aussi sur Discord."
      onClose={onClose}
      onSubmit={submit}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Annuler
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || (kind === 'app' && !appId)}>
            {busy ? 'Envoi…' : 'Envoyer'}
          </button>
        </>
      }
    >
      <div className="source-pick source-pick-3" role="radiogroup" aria-label="À propos de">
        {kinds.map((k) => (
          <button key={k.key} type="button" role="radio" aria-checked={kind === k.key} className={`source-chip ${kind === k.key ? 'on' : ''}`} onClick={() => setKind(k.key)}>
            {k.icon}
            {k.label}
            <small className="muted">{k.text}</small>
          </button>
        ))}
      </div>
      {kind === 'app' &&
        (apps.length ? (
          <label className="field">
            <span>App</span>
            <select value={appId} onChange={(e) => setAppId(Number(e.target.value))}>
              {apps.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name} · {a.nodeName}
                </option>
              ))}
            </select>
          </label>
        ) : (
          <p className="muted">Tu n’as pas encore d’app : choisis « Général ».</p>
        ))}
      <label className="field">
        <span>Sujet</span>
        <input value={subject} onChange={(e) => setSubject(e.target.value)} placeholder="Mon app répond 502" minLength={3} maxLength={120} required autoFocus />
      </label>
      <label className="field">
        <span>Message</span>
        <textarea value={body} onChange={(e) => setBody(e.target.value)} rows={6} maxLength={5000} placeholder="Ce que tu as fait, ce que tu vois, ce que tu attendais." required />
      </label>
      {error && <p className="error">{error}</p>}
    </Modal>
  )
}

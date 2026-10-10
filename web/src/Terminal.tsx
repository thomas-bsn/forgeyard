import { useEffect, useRef, useState } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'

/**
 * A shell in a container, through the server's WebSocket: keystrokes go as binary messages, the terminal's
 * output comes back the same way, and text messages carry its size and its end.
 */
export default function Terminal({ path }: { path: string }) {
  const box = useRef<HTMLDivElement>(null)
  const [status, setStatus] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [ended, setEnded] = useState('')
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const el = box.current
    if (!el) return
    const term = new XTerm({
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 13,
      theme: { background: '#0f1115', foreground: '#d6dae3', cursor: '#a5b4fc' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(el)
    fit.fit()

    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${window.location.host}${path}?cols=${term.cols}&rows=${term.rows}`)
    ws.binaryType = 'arraybuffer'
    setStatus('connecting')
    setEnded('')

    ws.onopen = () => {
      setStatus('open')
      term.focus()
    }
    ws.onmessage = (e) => {
      if (typeof e.data === 'string') {
        const msg = JSON.parse(e.data) as { type: string; code?: number; error?: string }
        if (msg.type === 'exit') {
          setEnded(msg.error || `Le shell s’est terminé (code ${msg.code ?? 0}).`)
        }
        return
      }
      term.write(new Uint8Array(e.data as ArrayBuffer))
    }
    ws.onclose = () => setStatus('closed')

    const encoder = new TextEncoder()
    const input = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
    })
    const resized = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    })
    const observer = new ResizeObserver(() => fit.fit())
    observer.observe(el)

    return () => {
      observer.disconnect()
      input.dispose()
      resized.dispose()
      ws.close()
      term.dispose()
    }
  }, [path, attempt])

  return (
    <section className="logs-panel terminal-panel">
      <div className="logs-head">
        <span className="live-status">
          <span className={`dot ${status === 'open' ? 'dot-up' : status === 'connecting' ? 'dot-warn' : ''}`} />
          {status === 'open' ? 'Connecté' : status === 'connecting' ? 'Connexion…' : 'Déconnecté'}
        </span>
        <span className="spacer" />
        {status === 'closed' && (
          <button type="button" className="btn btn-small" onClick={() => setAttempt((n) => n + 1)}>
            Nouveau terminal
          </button>
        )}
      </div>
      {ended && <div className="terminal-ended">{ended}</div>}
      <div className="terminal-box" ref={box} />
    </section>
  )
}

import { useEffect, useState } from 'react'

export default function App() {
  const [status, setStatus] = useState('...')

  useEffect(() => {
    fetch('/api/health')
      .then((res) => res.json())
      .then((data: { status: string }) => setStatus(data.status))
      .catch(() => setStatus('injoignable'))
  }, [])

  return (
    <main style={{ fontFamily: 'system-ui, sans-serif', padding: '2rem' }}>
      <h1>Forgeyard</h1>
      <p>API : {status}</p>
    </main>
  )
}

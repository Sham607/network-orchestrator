import { useEffect, useState } from 'react'

type Health = { status: string; time: string }

// Bootstrap placeholder: proves the SPA reaches the API through the same-origin
// /api path and renders a response. The real dashboard (tables, topology,
// charts) is built against the OpenAPI contract in openapi/openapi.yaml.
export default function App() {
  const [health, setHealth] = useState<Health | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetch('/api/v1/health')
      .then(async (res) => {
        if (!res.ok) throw new Error(`unexpected status ${res.status}`)
        return (await res.json()) as Health
      })
      .then(setHealth)
      .catch((err: unknown) => setError(String(err)))
  }, [])

  return (
    <main>
      <h1>Network Orchestrator</h1>
      {error && <p role="alert">API unreachable: {error}</p>}
      {health && <p>API health: {health.status}</p>}
      {!health && !error && <p>Checking API…</p>}
    </main>
  )
}

import { vi } from 'vitest'

export interface Call {
  method: string
  path: string
  body: unknown
}

type Route = (body: unknown) => { status?: number; json: unknown }

/**
 * Replaces global fetch with a table of routes ("PUT /api/campaigns/1/content")
 * and records every request. Unknown routes fail the test loudly.
 */
export function fakeFetch(routes: Record<string, Route>) {
  const calls: Call[] = []
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const path = String(input)
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ method, path, body })
    const route = routes[`${method} ${path}`]
    if (!route) throw new Error(`unexpected request ${method} ${path}`)
    const out = route(body)
    return new Response(JSON.stringify(out.json), { status: out.status ?? 200, headers: { 'Content-Type': 'application/json' } })
  })
  vi.stubGlobal('fetch', fn)
  return calls
}

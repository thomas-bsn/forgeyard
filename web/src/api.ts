export type User = {
  id: number
  username?: string
  displayName: string
  role: 'superadmin' | 'admin' | 'user'
}

export type Instance = {
  name: string
  setupRequired: boolean
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    const data = await res.json().catch(() => null)
    throw new ApiError(res.status, data?.error ?? `Erreur ${res.status}`)
  }
  return res.status === 204 ? (undefined as T) : res.json()
}

export const api = {
  instance: () => request<Instance>('GET', '/api/instance'),
  me: () => request<User>('GET', '/api/auth/me'),
  setup: (body: { token: string; instanceName: string; username: string; password: string }) =>
    request<User>('POST', '/api/setup', body),
  login: (username: string, password: string) =>
    request<User>('POST', '/api/auth/login', { username, password }),
  logout: () => request<void>('POST', '/api/auth/logout', {}),
}

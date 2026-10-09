export type Role = 'superadmin' | 'admin' | 'user'

export type User = {
  id: number
  username?: string
  displayName: string
  role: Role
}

export type Instance = {
  name: string
  setupRequired: boolean
  passwordLoginEnabled: boolean
  discordEnabled: boolean
  discordRedirectUrl: string
}

export type AccountRequest = {
  id: number
  discordId: string
  username: string
  displayName: string
  email?: string
  avatarUrl?: string
  createdAt: number
}

export type DiscordSettings = {
  clientId: string
  hasSecret: boolean
  redirectUrl: string
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
  setupDiscord: (body: { token: string; instanceName: string; clientId: string; clientSecret: string }) =>
    request<{ authorizeUrl: string }>('POST', '/api/setup/discord', body),
  login: (username: string, password: string) =>
    request<User>('POST', '/api/auth/login', { username, password }),
  logout: () => request<void>('POST', '/api/auth/logout', {}),
  requests: () => request<AccountRequest[]>('GET', '/api/admin/requests'),
  acceptRequest: (id: number, role: 'user' | 'admin') =>
    request<User>('POST', `/api/admin/requests/${id}/accept`, { role }),
  refuseRequest: (id: number, reason: string) => request<void>('POST', `/api/admin/requests/${id}/refuse`, { reason }),
  discordSettings: () => request<DiscordSettings>('GET', '/api/admin/settings/discord'),
  saveDiscordSettings: (clientId: string, clientSecret: string) =>
    request<DiscordSettings>('PUT', '/api/admin/settings/discord', { clientId, clientSecret }),
  loginSettings: () => request<{ passwordLogin: boolean }>('GET', '/api/admin/settings/login'),
  saveLoginSettings: (passwordLogin: boolean) =>
    request<{ passwordLogin: boolean }>('PUT', '/api/admin/settings/login', { passwordLogin }),
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

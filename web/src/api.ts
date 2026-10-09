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

export type NodeState = 'pending' | 'online' | 'offline'

export type Node = {
  id: number
  name: string
  state: NodeState
  hostname: string
  os: string
  arch: string
  cpus: number
  memoryBytes: number
  diskBytes: number
  dockerVersion: string
  agentVersion: string
  lastSeenAt?: number
  metrics?: {
    cpuPercent: number
    memoryUsedBytes: number
    diskUsedBytes: number
    containersRunning: number
  }
  cpuHistory?: number[]
}

export type JoinCommand = {
  node: Node
  command: string
  expiresAt: number
}

export type DomainMode = 'none' | 'wildcard' | 'cloudflare'

export type DomainSettings = {
  publicUrl: string
  discordRedirectUrl: string
  mode: DomainMode
  domain: string
  publicIp: string
  cloudflareHasToken: boolean
  cloudflareZone?: string
}

export type DomainCheck = {
  ok: boolean
  message: string
  name?: string
  resolved?: string[]
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
  setup: (body: { token: string; instanceName: string; publicUrl: string; username: string; password: string }) =>
    request<User>('POST', '/api/setup', body),
  setupDiscord: (body: { token: string; instanceName: string; publicUrl: string; clientId: string; clientSecret: string }) =>
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
  nodes: () => request<Node[]>('GET', '/api/admin/nodes'),
  createNode: (name: string) => request<JoinCommand>('POST', '/api/admin/nodes', { name }),
  newJoinCommand: (id: number) => request<JoinCommand>('POST', `/api/admin/nodes/${id}/join-command`, {}),
  deleteNode: (id: number) => request<void>('DELETE', `/api/admin/nodes/${id}`, {}),
  domainSettings: () => request<DomainSettings>('GET', '/api/admin/settings/domain'),
  saveDomainSettings: (body: { publicUrl: string; mode: DomainMode; domain: string; publicIp: string; cloudflareToken: string }) =>
    request<DomainSettings>('PUT', '/api/admin/settings/domain', body),
  checkDomain: () => request<DomainCheck>('POST', '/api/admin/settings/domain/check', {}),
  loginSettings: () => request<{ passwordLogin: boolean }>('GET', '/api/admin/settings/login'),
  saveLoginSettings: (passwordLogin: boolean) =>
    request<{ passwordLogin: boolean }>('PUT', '/api/admin/settings/login', { passwordLogin }),
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

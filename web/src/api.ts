export type Role = 'superadmin' | 'admin' | 'user'

export type User = {
  id: number
  username?: string
  displayName: string
  role: Role
  method: 'discord' | 'password'
  avatarUrl?: string
  customAvatar: boolean
  bannerUrl?: string
  customBanner: boolean
  accentColor?: string
  showApps: boolean
  showEmail: boolean
  notifyWebhookSet: boolean
  email: string
  bio: string
  discordName?: string
  nameFromDiscord: boolean
  createdAt: number
}

export type Member = {
  id: number
  displayName: string
  role: Role
  avatarUrl?: string
  bio: string
  publicApps: number
  createdAt: number
}

export type MemberProfile = Member & {
  bannerUrl?: string
  accentColor?: string
  discordId?: string
  email?: string
  showApps: boolean
  apps: { id: number; name: string; url?: string; state: AppState; logo: App['logo'] }[]
  activity: { at: number; kind: AppEvent['kind']; app: string; message: string }[]
}

export type NotifySettings = {
  webhookSet: boolean
  events: { requests: boolean; crashes: boolean; nodes: boolean }
}

export type Session = {
  id: string
  createdAt: number
  expiresAt: number
  ip: string
  userAgent: string
  current: boolean
}

export type Instance = {
  name: string
  setupRequired: boolean
  passwordLoginEnabled: boolean
  localNodeSupported: boolean
  localWebPorts?: 'busy' | 'free'
  localIp?: string
  appsLayout: 'sidebar' | 'nodes' | 'launcher'
  iconUrl?: string
  dnsProviders?: DNSProviderKind[]
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
  isLocal: boolean
  publicIp: string
  localIp: string
  dockerError?: string
  ingressMode: 'traefik' | 'proxy'
  ingressHttpPort: number
  metrics?: {
    cpuPercent: number
    memoryUsedBytes: number
    diskUsedBytes: number
    containersRunning: number
  }
  cpuHistory?: number[]
}

export type AppState =
  | 'pending'
  | 'pulling'
  | 'creating'
  | 'deploying'
  | 'running'
  | 'restarting'
  | 'stopped'
  | 'exited'
  | 'error'
  | 'node-offline'

export type App = {
  id: number
  name: string
  ownerId: number
  ownerName: string
  nodeId: number
  nodeName: string
  image: string
  port: number
  memoryMb: number
  running: boolean
  url?: string
  state: AppState
  error?: string
  exitCode?: number
  oomKilled?: boolean
  restartCount?: number
  startedAt?: number
  hostPort?: number
  cpuPercent?: number
  memoryUsedBytes?: number
  suspended?: boolean
  public: boolean
  crashSuspended?: boolean
  logo: { mode: 'auto' | 'custom' | 'initial'; url?: string; color?: string; autoUrl?: string }
  updatedAt: number
  env?: Record<string, string>
}

export type AppEvent = { at: number; kind: 'info' | 'success' | 'warning' | 'error'; message: string }

export type AppUsage = {
  memoryLimitBytes: number
  samples: { t: number; cpu: number; mem: number }[]
}

/** A container of a node that Forgeyard did not create, shown with the apps and owned by the superadmin. */
export type Container = {
  nodeId: number
  nodeName: string
  id: string
  name: string
  image: string
  state: string
  status: string
  createdAt: number
  ports: string[]
  cpuPercent?: number
  memoryUsedBytes?: number
  composeProject?: string
  ownerName: string
  logoUrl?: string
}

export type Account = {
  id: number
  displayName: string
  method: 'discord' | 'password'
  role: 'superadmin' | 'admin' | 'user'
  disabled: boolean
  appsSuspended: boolean
  appCount: number
  createdAt: number
  avatarUrl?: string
}

export type AppInput = {
  name?: string
  image: string
  port: number
  memoryMb: number
  env: Record<string, string>
}

export type JoinCommand = {
  node: Node
  command: string
  dockerCommand: string
  agentServer: string
  expiresAt: number
}

export type DomainMode = 'none' | 'wildcard' | 'provider'

export type DomainChoice = {
  mode: DomainMode
  domain: string
  publicIp: string
  provider: string
  credentials: Record<string, string>
}

export type IngressChoice = { mode: 'traefik' | 'proxy'; httpPort: number }

export type DNSProviderKind = {
  name: string
  label: string
  docsUrl: string
  help: string
  fields: { key: string; label: string; secret: boolean; placeholder?: string }[]
}

export type DomainSettings = {
  publicUrl: string
  discordRedirectUrl: string
  mode: DomainMode
  domain: string
  publicIp: string
  provider: string
  zone?: string
  credentials: Record<string, string>
  secretsSet: string[]
  providers: DNSProviderKind[]
  warning?: string
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
  // Some answers have no body (204, or 202 for an action the node carries out later).
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

export const api = {
  instance: () => request<Instance>('GET', '/api/instance'),
  me: () => request<User>('GET', '/api/auth/me'),
  setup: (body: {
    token: string
    instanceName: string
    publicUrl: string
    username: string
    password: string
    localNode: boolean
    domain: DomainChoice
    ingress: IngressChoice
    icon?: string
  }) =>
    request<User>('POST', '/api/setup', body),
  setupDiscord: (body: {
    token: string
    instanceName: string
    publicUrl: string
    clientId: string
    clientSecret: string
    localNode: boolean
    domain: DomainChoice
    ingress: IngressChoice
    icon?: string
  }) =>
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
  apps: () => request<App[]>('GET', '/api/apps'),
  app: (id: number) => request<App>('GET', `/api/apps/${id}`),
  createApp: (input: AppInput) => request<App>('POST', '/api/apps', input),
  updateApp: (id: number, input: AppInput) => request<App>('PUT', `/api/apps/${id}`, input),
  appAction: (id: number, action: 'start' | 'stop' | 'redeploy') => request<App>('POST', `/api/apps/${id}/${action}`, {}),
  deleteApp: (id: number) => request<void>('DELETE', `/api/apps/${id}`, {}),
  appEvents: (id: number) => request<AppEvent[]>('GET', `/api/apps/${id}/events`),
  appUsage: (id: number) => request<AppUsage>('GET', `/api/apps/${id}/usage`),
  containerUsage: (c: Container) => request<AppUsage>('GET', `/api/admin/nodes/${c.nodeId}/containers/${c.id}/usage`),
  containerEvents: (c: Container) => request<AppEvent[]>('GET', `/api/admin/nodes/${c.nodeId}/containers/${c.id}/events`),
  containers: () => request<Container[]>('GET', '/api/admin/containers'),
  containerAction: (c: Container, action: 'start' | 'stop' | 'restart') =>
    request<void>('POST', `/api/admin/nodes/${c.nodeId}/containers/${c.id}/${action}`, {}),
  users: () => request<Account[]>('GET', '/api/admin/users'),
  updateUser: (id: number, body: { role?: 'user' | 'admin'; disabled?: boolean; appsSuspended?: boolean }) =>
    request<void>('PUT', `/api/admin/users/${id}`, body),
  deleteUser: (id: number) => request<void>('DELETE', `/api/admin/users/${id}`, {}),
  saveProfile: (body: { displayName: string; nameFromDiscord: boolean; bio: string; email: string; showApps: boolean; showEmail: boolean }) =>
    request<User>('PUT', '/api/me/profile', body),
  uploadAvatar: (image: string) => request<User>('PUT', '/api/me/avatar', { image }),
  deleteAvatar: () => request<User>('DELETE', '/api/me/avatar', {}),
  uploadBanner: (image: string) => request<User>('PUT', '/api/me/banner', { image }),
  deleteBanner: () => request<User>('DELETE', '/api/me/banner', {}),
  members: () => request<Member[]>('GET', '/api/members'),
  member: (id: number) => request<MemberProfile>('GET', `/api/members/${id}`),
  setAppLogo: (id: number, body: { mode: 'auto' | 'custom' | 'initial'; color?: string; image?: string }) =>
    request<App>('PUT', `/api/apps/${id}/logo`, body),
  setAppPublic: (id: number, value: boolean) => request<App>('PUT', `/api/apps/${id}/public`, { public: value }),
  changePassword: (current: string, next: string) => request<void>('PUT', '/api/me/password', { current, new: next }),
  sessions: () => request<Session[]>('GET', '/api/me/sessions'),
  deleteSession: (id: string) => request<void>('DELETE', `/api/me/sessions/${id}`, {}),
  uploadIcon: (image: string) => request<{ iconUrl: string }>('PUT', '/api/admin/settings/icon', { image }),
  deleteIcon: () => request<void>('DELETE', '/api/admin/settings/icon', {}),
  notifySettings: () => request<NotifySettings>('GET', '/api/admin/settings/notifications'),
  saveNotifySettings: (body: { webhook?: string; clear?: boolean; events: NotifySettings['events'] }) =>
    request<NotifySettings>('PUT', '/api/admin/settings/notifications', body),
  testNotify: () => request<void>('POST', '/api/admin/settings/notifications/test', {}),
  saveMyWebhook: (webhook: string) => request<User>('PUT', '/api/me/notifications', { webhook }),
  testMyWebhook: () => request<void>('POST', '/api/me/notifications/test', {}),
  saveGeneralSettings: (name: string, publicUrl: string, appsLayout: Instance['appsLayout']) =>
    request<{ name: string; publicUrl: string; appsLayout: Instance['appsLayout'] }>('PUT', '/api/admin/settings/general', { name, publicUrl, appsLayout }),
  nodes: () => request<Node[]>('GET', '/api/admin/nodes'),
  setNodeIngress: (id: number, body: { publicIp: string; ingressMode: 'traefik' | 'proxy'; ingressHttpPort: number }) =>
    request<Node>('PUT', `/api/admin/nodes/${id}/ingress`, body),
  createNode: (name: string, local: boolean) => request<JoinCommand>('POST', '/api/admin/nodes', { name, local }),
  newJoinCommand: (id: number) => request<JoinCommand>('POST', `/api/admin/nodes/${id}/join-command`, {}),
  deleteNode: (id: number) => request<void>('DELETE', `/api/admin/nodes/${id}`, {}),
  domainSettings: () => request<DomainSettings>('GET', '/api/admin/settings/domain'),
  saveDomainSettings: (body: {
    publicUrl: string
    mode: DomainMode
    domain: string
    publicIp: string
    provider: string
    credentials: Record<string, string>
  }) =>
    request<DomainSettings>('PUT', '/api/admin/settings/domain', body),
  checkDomain: () => request<DomainCheck>('POST', '/api/admin/settings/domain/check', {}),
  loginSettings: () => request<{ passwordLogin: boolean }>('GET', '/api/admin/settings/login'),
  saveLoginSettings: (passwordLogin: boolean) =>
    request<{ passwordLogin: boolean }>('PUT', '/api/admin/settings/login', { passwordLogin }),
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

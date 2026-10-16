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
  supportWebhookSet: boolean
  events: { requests: boolean; support: boolean; crashes: boolean; nodes: boolean; backups: boolean }
}

export type PathStep = {
  key: 'visitor' | 'dns' | 'entry' | 'relay' | 'traefik' | 'container' | 'gateway' | 'node'
  title: string
  detail: string
  state: 'ok' | 'warn' | 'error' | 'info' | 'unknown'
  note: string
}

export type Diagnosis = { level: 'error' | 'warn'; message: string; help?: string; fixPort?: number }

export type TopoEndpoint = { network: string; subnet?: string; ip?: string; aliases?: string[] }
export type TopoPort = { containerPort: number; hostPort: number; protocol: string }

export type AppNetwork = {
  url?: string
  kind: App['kind']
  steps: PathStep[]
  issues: Diagnosis[]
  checkedAt: number
  networks: TopoEndpoint[]
  neighbors: string[]
  others: number
  listening: number[]
  published: TopoPort[]
  routePort?: number
  measured: boolean
}

export type ProbeResult = { ok: boolean; status?: number; error?: string; ms: number }

export type TopoContainer = {
  id: string
  name: string
  image: string
  state: string
  role: 'app' | 'sandbox' | 'traefik' | 'server' | 'agent' | 'external'
  appId?: number
  ownerName?: string
  url?: string
  routePort?: number
  listening: number[]
  published: TopoPort[]
  endpoints: TopoEndpoint[]
  composeProject?: string
  issue?: Diagnosis
  steps?: PathStep[]
  logoUrl?: string
  logoColor?: string
}

export type TopoNode = {
  id: number
  name: string
  state: 'online' | 'offline'
  isLocal: boolean
  ingressMode: 'traefik' | 'proxy' | 'relay' | 'tunnel' // relay, tunnel: through Forgeyard's machine
  httpPort: number
  localIp?: string
  publicIp?: string
  relayedBy?: number
  measured: boolean
  networks: { name: string; driver: string; subnet?: string; internal?: boolean }[]
  containers: TopoContainer[]
}

export type Topology = { domain?: string; publicIp?: string; sshPort?: number; nodes: TopoNode[]; checkedAt: number }

export type SSHKey = { id: number; name: string; type: string; fingerprint: string; createdAt: number; lastUsedAt?: number }

export type TicketKind = 'general' | 'app' | 'infra'

export type Ticket = {
  id: number
  kind: TicketKind
  appId?: number
  appName?: string
  subject: string
  status: 'open' | 'closed'
  waiting: boolean
  authorId: number
  authorName: string
  messages: number
  createdAt: number
  updatedAt: number
}

export type TicketDetail = Ticket & {
  thread: { id: number; authorId?: number; authorName: string; avatarUrl?: string; staff: boolean; body: string; createdAt: number }[]
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
  appsDomain?: string
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
  agentVersion: string // the commit it was built from, empty for a development build
  agentOutdated?: boolean
  selfUpdate?: boolean
  updating?: boolean
  updateStep?: 'download' | 'restart'
  updateError?: string
  gpus: NodeGpus
  lastSeenAt?: number
  isLocal: boolean
  publicIp: string
  localIp: string
  dockerError?: string
  ingressMode: 'traefik' | 'proxy' | 'relay' | 'tunnel' // relay, tunnel: through Forgeyard's machine
  ingressHttpPort: number
  lan: boolean // on Forgeyard's network
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
  | 'building'
  | 'stopped'
  | 'exited'
  | 'error'
  | 'node-offline'
  // Services of a stack: waiting for their dependencies, blocked by one that failed; a job completed or failed.
  | 'waiting'
  | 'blocked'
  | 'completed'
  | 'failed'

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
  cpuPercent?: number // in percent of one core: 250 is 2.5 cores busy
  cpuCores?: number // the node's cores
  cpuLimit?: number // the container's CPU limit in cores, when it has one
  memoryUsedBytes?: number
  suspended?: boolean
  public: boolean
  crashSuspended?: boolean
  movingFrom?: number
  listeningPorts?: number[] // what it really listens on, while running
  kind: 'web' | 'sandbox' | 'service' | 'job' // service and job: in a stack, not exposed
  stackId?: number
  gpu?: GpuRequest
  gpuNames?: string[] // the GPUs it may use, as its node names them
  service?: string // its name in its stack's compose file
  waitingFor?: string // what a service of a stack waits for to start
  ssh?: string // the command reaching it through the SSH gateway
  sftp?: string // the same for SFTP
  // Paths kept across redeployments, with their size (-1 not measured yet); builtin: a sandbox's /root.
  volumes: { path: string; sizeBytes: number; builtin?: boolean }[]
  logo: { mode: 'auto' | 'custom' | 'initial'; url?: string; color?: string; autoUrl?: string }
  updatedAt: number
  env?: Record<string, string>
  dockerfile?: string
}

export type ComposeError = { line: number; message: string }

export type BackupRestore = { id: number; snapshot: string; volume: string; status: 'running' | 'ok' | 'error'; error?: string; createdAt: number; inUse: boolean }

export type BackupPolicy = {
  id: number
  volume: string
  schedule: 'hourly' | 'daily'
  hour: number
  keep: number
  enabled: boolean
  lastRunAt?: number
  lastStatus?: 'running' | 'ok' | 'error'
  lastError?: string
  lastSnapshot?: string
  nextRunAt?: number
  restores: BackupRestore[]
}

export type StackVolume = {
  name: string
  docker: string
  sizeBytes: number
  services: string[]
  postgres?: string // the postgres service using it: saved with pg_dump
  restored?: boolean
  backup?: BackupPolicy
}

export type StackState = 'pending' | 'running' | 'deploying' | 'stopped' | 'error' | 'blocked' | 'node-offline'

export type Stack = {
  id: number
  name: string
  ownerId: number
  ownerName: string
  nodeId: number
  nodeName: string
  url?: string
  state: StackState
  services: App[]
  volumes: StackVolume[]
  updatedAt: number
  compose?: string
  env?: Record<string, string>
}

export type StackPreview = {
  services: { name: string; app: string; image: string; kind: App['kind']; port?: number; dependsOn: { service: string; condition: string }[]; backup?: string }[]
  volumes: string[]
  errors: ComposeError[]
  error?: string
}

export type Snapshot = { id: string; time: number; sizeBytes: number }

export type BackupSettings = {
  endpoint: string
  bucket: string
  prefix: string
  region: string
  accessKey: string
  secretSet: boolean
  password?: string
  configured: boolean
}

export type AppEvent = { at: number; kind: 'info' | 'success' | 'warning' | 'error'; message: string }

export type AppUsage = {
  memoryLimitBytes: number
  samples: { t: number; cpu: number; mem: number }[]
  // The GPUs the app may use, each as a whole (shared with the other apps using it), over the last hour.
  gpus?: (Gpu & { samples: { t: number; util: number; mem: number; memTotal: number; temp?: number; power?: number }[] })[]
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
  cpuPercent?: number // in percent of one core: 250 is 2.5 cores busy
  cpuCores?: number // the node's cores
  cpuLimit?: number // the container's CPU limit in cores, when it has one
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
  kind?: 'web' | 'sandbox' // at creation
  volumes?: string[]
  image: string
  dockerfile?: string
  port: number
  memoryMb: number
  env: Record<string, string>
  nodeId?: number
  gpu?: GpuRequest
}

/** The GPUs of its node an app may use: all, or some by ID. Access only, not a reservation. */
export type GpuRequest = { vendor: 'nvidia'; all: boolean; ids?: string[] }

export type Gpu = { vendor: string; index: number; id: string; name: string; memoryBytes?: number }

/** What a node's Docker gives containers: ok, unsupported (with why), error, or '' not checked yet. */
export type NodeGpus = { status: '' | 'ok' | 'unsupported' | 'error'; error?: string; gpus: Gpu[] }

export type NodeChoice = {
  id: number
  name: string
  cpus: number
  memoryBytes: number
  freeMemoryBytes: number
  apps: number
  recommended: boolean
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
    // The problems of a compose file, with their lines, when it was refused.
    public composeErrors?: ComposeError[],
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
    throw new ApiError(res.status, data?.error ?? `Erreur ${res.status}`, data?.errors)
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
  gpus: () => request<(NodeGpus & { nodeId: number; nodeName: string })[]>('GET', '/api/gpus'),
  imagePorts: (image: string) => request<{ ports: number[]; volumes: string[]; volumesFrom?: 'image' | 'known'; hub?: string; error?: string }>('GET', `/api/images/ports?image=${encodeURIComponent(image)}`),
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
  nodeChoices: () => request<NodeChoice[]>('GET', '/api/admin/nodes/choices'),
  moveApp: (id: number, nodeId: number, leaveData = false) => request<App>('POST', `/api/admin/apps/${id}/move`, { nodeId, leaveData }),
  setAppPublic: (id: number, value: boolean) => request<App>('PUT', `/api/apps/${id}/public`, { public: value }),
  changePassword: (current: string, next: string) => request<void>('PUT', '/api/me/password', { current, new: next }),
  sessions: () => request<Session[]>('GET', '/api/me/sessions'),
  deleteSession: (id: string) => request<void>('DELETE', `/api/me/sessions/${id}`, {}),
  uploadIcon: (image: string) => request<{ iconUrl: string }>('PUT', '/api/admin/settings/icon', { image }),
  deleteIcon: () => request<void>('DELETE', '/api/admin/settings/icon', {}),
  notifySettings: () => request<NotifySettings>('GET', '/api/admin/settings/notifications'),
  saveNotifySettings: (body: { webhook?: string; clear?: boolean; supportWebhook?: string; clearSupport?: boolean; events: NotifySettings['events'] }) =>
    request<NotifySettings>('PUT', '/api/admin/settings/notifications', body),
  updateAgent: (nodeId: number) => request<Node>('POST', `/api/admin/nodes/${nodeId}/update-agent`, {}),
  agentSettings: () => request<{ autoUpdate: boolean; serverVersion: string }>('GET', '/api/admin/settings/agents'),
  saveAgentSettings: (autoUpdate: boolean) => request<{ autoUpdate: boolean; serverVersion: string }>('PUT', '/api/admin/settings/agents', { autoUpdate }),
  testNotify: () => request<void>('POST', '/api/admin/settings/notifications/test', {}),
  testSupportNotify: () => request<void>('POST', '/api/admin/settings/notifications/test-support', {}),
  appNetwork: (id: number) => request<AppNetwork>('GET', `/api/apps/${id}/network`),
  probeApp: (id: number) => request<ProbeResult>('POST', `/api/apps/${id}/network/test`, {}),
  topology: () => request<Topology>('GET', '/api/admin/topology'),
  sshKeys: () => request<SSHKey[]>('GET', '/api/me/ssh-keys'),
  addSSHKey: (publicKey: string, name: string) => request<SSHKey>('POST', '/api/me/ssh-keys', { publicKey, name }),
  deleteSSHKey: (id: number) => request<void>('DELETE', `/api/me/ssh-keys/${id}`, {}),
  tickets: () => request<Ticket[]>('GET', '/api/support/tickets'),
  ticket: (id: number) => request<TicketDetail>('GET', `/api/support/tickets/${id}`),
  createTicket: (body: { kind: TicketKind; appId?: number; subject: string; body: string }) => request<TicketDetail>('POST', '/api/support/tickets', body),
  replyTicket: (id: number, body: string) => request<TicketDetail>('POST', `/api/support/tickets/${id}/messages`, { body }),
  setTicketStatus: (id: number, status: Ticket['status']) => request<TicketDetail>('PUT', `/api/support/tickets/${id}/status`, { status }),
  saveMyWebhook: (webhook: string) => request<User>('PUT', '/api/me/notifications', { webhook }),
  testMyWebhook: () => request<void>('POST', '/api/me/notifications/test', {}),
  saveGeneralSettings: (name: string, publicUrl: string, appsLayout: Instance['appsLayout']) =>
    request<{ name: string; publicUrl: string; appsLayout: Instance['appsLayout'] }>('PUT', '/api/admin/settings/general', { name, publicUrl, appsLayout }),
  nodes: () => request<Node[]>('GET', '/api/admin/nodes'),
  setNodeIngress: (id: number, body: { publicIp: string; ingressMode: 'traefik' | 'proxy' | 'relay' | 'tunnel'; ingressHttpPort: number }) =>
    request<Node>('PUT', `/api/admin/nodes/${id}/ingress`, body),
  createNode: (body: { name: string; local: boolean; lan?: boolean; ingressMode?: Node['ingressMode']; publicIp?: string; ingressHttpPort?: number }) =>
    request<JoinCommand>('POST', '/api/admin/nodes', body),
  checkNode: (id: number) =>
    request<{ connected: boolean; traefik: 'ok' | 'down' | 'unknown'; reach: 'ok' | 'error' | 'skip'; reachTarget?: string; reachDetail?: string }>('POST', `/api/admin/nodes/${id}/check`, {}),
  newJoinCommand: (id: number) => request<JoinCommand>('POST', `/api/admin/nodes/${id}/join-command`, {}),
  deleteNode: (id: number) => request<{ cleaned: boolean; detail?: string }>('DELETE', `/api/admin/nodes/${id}`, {}),
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
  stacks: () => request<Stack[]>('GET', '/api/stacks'),
  stack: (id: number) => request<Stack>('GET', `/api/stacks/${id}`),
  validateStack: (body: { name: string; compose: string; env: Record<string, string> }) => request<StackPreview>('POST', '/api/stacks/validate', body),
  createStack: (body: { name: string; compose: string; env: Record<string, string>; nodeId?: number }) => request<Stack>('POST', '/api/stacks', body),
  updateStack: (id: number, body: { compose: string; env: Record<string, string> }) => request<Stack>('PUT', `/api/stacks/${id}`, body),
  stackAction: (id: number, action: 'start' | 'stop' | 'redeploy') => request<Stack>('POST', `/api/stacks/${id}/${action}`, {}),
  deleteStack: (id: number, volumes: boolean) => request<void>('DELETE', `/api/stacks/${id}${volumes ? '?volumes=1' : ''}`, {}),
  saveBackupPolicy: (id: number, volume: string, body: { schedule: BackupPolicy['schedule']; hour: number; keep: number; enabled: boolean }) =>
    request<BackupPolicy>('PUT', `/api/stacks/${id}/backups/${encodeURIComponent(volume)}`, body),
  deleteBackupPolicy: (id: number, volume: string) => request<void>('DELETE', `/api/stacks/${id}/backups/${encodeURIComponent(volume)}`, {}),
  runBackup: (id: number, volume: string) => request<void>('POST', `/api/stacks/${id}/backups/${encodeURIComponent(volume)}/run`, {}),
  snapshots: (id: number, volume: string) => request<Snapshot[]>('GET', `/api/stacks/${id}/backups/${encodeURIComponent(volume)}/snapshots`),
  restoreBackup: (id: number, volume: string, snapshot: string) =>
    request<BackupRestore>('POST', `/api/stacks/${id}/backups/${encodeURIComponent(volume)}/restore`, { snapshot }),
  useRestore: (id: number, volume: string, restore: number) =>
    request<Stack>('POST', `/api/stacks/${id}/backups/${encodeURIComponent(volume)}/restores/${restore}/use`, { confirm: true }),
  backupSettings: () => request<BackupSettings>('GET', '/api/admin/settings/backups'),
  saveBackupSettings: (body: { endpoint: string; bucket: string; prefix: string; region: string; accessKey: string; secretKey: string; password: string }) =>
    request<BackupSettings>('PUT', '/api/admin/settings/backups', body),
  testBackupSettings: () => request<{ ok: boolean; error?: string; log?: string }>('POST', '/api/admin/settings/backups/test', {}),
  discordBot: () => request<{ set: boolean; name?: string }>('GET', '/api/admin/settings/discord/bot'),
  saveDiscordBot: (token: string) => request<{ set: boolean; name?: string }>('PUT', '/api/admin/settings/discord/bot', { token }),
  deleteDiscordBot: () => request<{ set: boolean; name?: string }>('DELETE', '/api/admin/settings/discord/bot', {}),
  refreshMyDiscord: () => request<User>('POST', '/api/me/discord/refresh', {}),
  loginSettings: () => request<{ passwordLogin: boolean }>('GET', '/api/admin/settings/login'),
  saveLoginSettings: (passwordLogin: boolean) =>
    request<{ passwordLogin: boolean }>('PUT', '/api/admin/settings/login', { passwordLogin }),
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

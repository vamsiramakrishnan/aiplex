# 09 — Console UI

## Overview

The AIPlex Console is a React SPA that provides a unified management interface for all three planes. It talks to the AIPlex API for data and actions, and to Keycloak for authentication. The Console is served as static files from the AIPlex API pod.

---

## Component Architecture

```
App.tsx
├── AuthProvider (Keycloak OIDC)
├── Layout
│   ├── Sidebar / TopNav
│   │   └── PlaneSelector.tsx (MCPlex / A2APlex / LLMPlex / Agents / Dashboard)
│   └── MainContent
│       ├── MCPlex.tsx
│       │   ├── CatalogView (browse + search templates)
│       │   │   ├── TemplateCard (per template)
│       │   │   └── DeployModal (config form + deploy button)
│       │   ├── InstancesView (running MCP servers)
│       │   │   ├── InstanceRow (status, tools, health)
│       │   │   └── InstanceDetail (logs, config, scale)
│       │   └── PermissionsView (tool-level access)
│       │       └── ScopeSelector.tsx
│       ├── A2APlex.tsx
│       │   ├── CatalogView
│       │   ├── InstancesView
│       │   │   └── AgentCard.tsx (A2A Agent Card display)
│       │   └── PermissionsView
│       ├── LLMPlex.tsx
│       │   ├── ProvidersView (model endpoints + API keys)
│       │   ├── RoutingView (failover rules, weights)
│       │   └── PermissionsView
│       ├── Agents.tsx
│       │   └── CrossPlaneView (what can this agent access?)
│       ├── Deploy.tsx (unified deploy form)
│       │   └── ConfigForm (rendered from JSON Schema)
│       ├── Permissions.tsx (unified permission editor)
│       └── Dashboard.tsx
│           ├── MetricsCards (tool calls, delegations, LLM requests)
│           ├── PolicyDenials (recent denied requests)
│           ├── CostTracker (LLM token usage + cost)
│           └── ActiveSessions (current agent sessions)
```

---

## Technology Stack

| Layer | Choice | Rationale |
|-------|--------|-----------|
| Framework | React 19 | Industry standard, large ecosystem |
| Language | TypeScript | Type safety for API contracts |
| Routing | React Router v7 | Standard routing solution |
| State | TanStack Query (React Query) | Server state caching, auto-refetch |
| UI Components | Shadcn/ui + Tailwind CSS | Accessible, customizable, no vendor lock |
| Forms | React Hook Form + Zod | JSON Schema → Zod → form validation |
| Auth | keycloak-js | Official Keycloak adapter |
| Charts | Recharts | Lightweight, React-native charting |
| Build | Vite | Fast dev server, optimized builds |

---

## Authentication Flow

```typescript
// App.tsx

import Keycloak from 'keycloak-js';

const keycloak = new Keycloak({
  url: import.meta.env.VITE_KEYCLOAK_URL,
  realm: 'aiplex',
  clientId: 'aiplex-console',
});

function App() {
  const [authenticated, setAuthenticated] = useState(false);

  useEffect(() => {
    keycloak.init({
      onLoad: 'login-required',
      pkceMethod: 'S256',
      checkLoginIframe: false,
    }).then(auth => {
      setAuthenticated(auth);
      // Auto-refresh token before expiry
      setInterval(() => keycloak.updateToken(60), 30000);
    });
  }, []);

  if (!authenticated) return <LoadingScreen />;

  return (
    <AuthContext.Provider value={{ keycloak }}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </AuthContext.Provider>
  );
}
```

---

## Key Pages

### MCPlex Page

```typescript
// pages/MCPlex.tsx

function MCPlex() {
  const [tab, setTab] = useState<'catalog' | 'instances' | 'permissions'>('catalog');

  return (
    <div>
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="catalog">Catalog</TabsTrigger>
          <TabsTrigger value="instances">Instances</TabsTrigger>
          <TabsTrigger value="permissions">Permissions</TabsTrigger>
        </TabsList>

        <TabsContent value="catalog">
          <CatalogView plane="mcplex" />
        </TabsContent>
        <TabsContent value="instances">
          <InstancesView plane="mcplex" />
        </TabsContent>
        <TabsContent value="permissions">
          <PermissionsView plane="mcplex" />
        </TabsContent>
      </Tabs>
    </div>
  );
}
```

### Catalog View (Shared)

```typescript
function CatalogView({ plane }: { plane: string }) {
  const [search, setSearch] = useState('');
  const [category, setCategory] = useState<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ['catalog', plane, search, category],
    queryFn: () => api.getCatalog(plane, { query: search, category }),
  });

  const deployMutation = useMutation({
    mutationFn: (params: DeployParams) => api.deploy(params),
    onSuccess: () => queryClient.invalidateQueries(['instances', plane]),
  });

  return (
    <div>
      <SearchBar value={search} onChange={setSearch} />
      <CategoryFilter value={category} onChange={setCategory} />

      <div className="grid grid-cols-3 gap-4">
        {data?.templates.map(template => (
          <TemplateCard
            key={template.id}
            template={template}
            onDeploy={() => setDeployTarget(template)}
          />
        ))}
      </div>

      {deployTarget && (
        <DeployModal
          template={deployTarget}
          onDeploy={(config) => deployMutation.mutate({
            plane,
            template_id: deployTarget.id,
            config,
          })}
          onClose={() => setDeployTarget(null)}
        />
      )}
    </div>
  );
}
```

### Deploy Modal (JSON Schema → Form)

```typescript
function DeployModal({ template, onDeploy, onClose }) {
  // Convert JSON Schema to Zod schema for validation
  const zodSchema = jsonSchemaToZod(template.config_schema);
  const form = useForm({ resolver: zodResolver(zodSchema) });

  return (
    <Dialog open onClose={onClose}>
      <DialogTitle>Deploy {template.name}</DialogTitle>
      <DialogContent>
        <form onSubmit={form.handleSubmit(onDeploy)}>
          {/* Dynamically render form fields from JSON Schema */}
          <JsonSchemaForm
            schema={template.config_schema}
            form={form}
          />
          <Button type="submit">Deploy</Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function JsonSchemaForm({ schema, form }) {
  return Object.entries(schema.properties || {}).map(([key, prop]) => {
    const field = prop as JsonSchemaProperty;
    return (
      <FormField key={key} name={key} control={form.control}>
        <FormLabel>
          {field.description || key}
          {schema.required?.includes(key) && ' *'}
        </FormLabel>
        {field.type === 'string' && <Input {...form.register(key)} />}
        {field.type === 'integer' && <Input type="number" {...form.register(key, { valueAsNumber: true })} />}
        {field.type === 'boolean' && <Switch {...form.register(key)} />}
        {field.enum && (
          <Select {...form.register(key)}>
            {field.enum.map(v => <option key={v} value={v}>{v}</option>)}
          </Select>
        )}
      </FormField>
    );
  });
}
```

### Cross-Plane Agent View

```typescript
// pages/Agents.tsx

function AgentDetail({ agentId }: { agentId: string }) {
  const { data: agent } = useQuery({
    queryKey: ['agent', agentId],
    queryFn: () => api.getAgent(agentId),
  });

  return (
    <div>
      <h2>{agent.name}</h2>
      <StatusBadge status={agent.status} />

      <h3>MCPlex — Tools</h3>
      <ScopeList scopes={agent.scopes.filter(s => s.startsWith('mcp:'))} />

      <h3>A2APlex — Agent Delegation</h3>
      <ScopeList scopes={agent.scopes.filter(s => s.startsWith('a2a:'))} />

      <h3>LLMPlex — Models</h3>
      <ScopeList scopes={agent.scopes.filter(s => s.startsWith('llm:'))} />
    </div>
  );
}
```

---

## Shared Components

### PlaneSelector

```typescript
// components/PlaneSelector.tsx

function PlaneSelector() {
  const location = useLocation();
  const planes = [
    { id: 'mcplex', label: 'MCPlex', icon: ToolIcon, path: '/mcplex' },
    { id: 'a2aplex', label: 'A2APlex', icon: AgentsIcon, path: '/a2aplex' },
    { id: 'llmplex', label: 'LLMPlex', icon: ModelIcon, path: '/llmplex' },
    { id: 'agents', label: 'Agents', icon: ShieldIcon, path: '/agents' },
    { id: 'dashboard', label: 'Dashboard', icon: ChartIcon, path: '/dashboard' },
  ];

  return (
    <nav>
      {planes.map(p => (
        <NavLink
          key={p.id}
          to={p.path}
          className={cn('nav-item', location.pathname.startsWith(p.path) && 'active')}
        >
          <p.icon />
          <span>{p.label}</span>
        </NavLink>
      ))}
    </nav>
  );
}
```

### ScopeSelector

```typescript
// components/ScopeSelector.tsx

function ScopeSelector({ 
  availableScopes, 
  selectedScopes, 
  onChange 
}: {
  availableScopes: Scope[];
  selectedScopes: string[];
  onChange: (scopes: string[]) => void;
}) {
  const grouped = groupBy(availableScopes, s => s.name.split(':')[0]);

  return (
    <div>
      {Object.entries(grouped).map(([plane, scopes]) => (
        <div key={plane}>
          <h4>{plane.toUpperCase()}</h4>
          {scopes.map(scope => (
            <label key={scope.name} className="flex items-center gap-2">
              <Checkbox
                checked={selectedScopes.includes(scope.name)}
                onCheckedChange={(checked) => {
                  onChange(checked
                    ? [...selectedScopes, scope.name]
                    : selectedScopes.filter(s => s !== scope.name)
                  );
                }}
              />
              <span>{scope.description}</span>
              <code className="text-xs">{scope.name}</code>
            </label>
          ))}
        </div>
      ))}
    </div>
  );
}
```

### StatusBadge

```typescript
// components/StatusBadge.tsx

const STATUS_CONFIG = {
  running: { color: 'green', label: 'Running', icon: CheckCircle },
  provisioning: { color: 'yellow', label: 'Provisioning', icon: Loader },
  degraded: { color: 'orange', label: 'Degraded', icon: AlertTriangle },
  stopped: { color: 'gray', label: 'Stopped', icon: PauseCircle },
  failed: { color: 'red', label: 'Failed', icon: XCircle },
  terminated: { color: 'gray', label: 'Terminated', icon: Trash },
};

function StatusBadge({ status }: { status: string }) {
  const config = STATUS_CONFIG[status] || STATUS_CONFIG.failed;
  const Icon = config.icon;
  return (
    <Badge variant={config.color}>
      <Icon className="w-3 h-3" />
      {config.label}
    </Badge>
  );
}
```

---

## API Client

```typescript
// lib/api.ts

class AiplexAPI {
  constructor(private baseUrl: string, private getToken: () => string) {}

  private async fetch<T>(path: string, options?: RequestInit): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${this.getToken()}`,
        ...options?.headers,
      },
    });

    if (!response.ok) {
      const error = await response.json();
      throw new APIError(error.error.code, error.error.message);
    }

    return response.json();
  }

  // Catalog
  getCatalog(plane: string, params?: CatalogParams) {
    const qs = new URLSearchParams(params as any).toString();
    return this.fetch<CatalogPage>(`/api/v1/catalog/${plane}?${qs}`);
  }

  // Instances
  getInstances(plane: string) {
    return this.fetch<Instance[]>(`/api/v1/instances?plane=${plane}`);
  }

  getInstance(id: string) {
    return this.fetch<Instance>(`/api/v1/instances/${id}`);
  }

  // Deploy
  deploy(params: DeployParams) {
    return this.fetch<Instance>('/api/v1/deploy', {
      method: 'POST',
      body: JSON.stringify(params),
    });
  }

  undeploy(id: string) {
    return this.fetch<void>(`/api/v1/instances/${id}`, { method: 'DELETE' });
  }

  // Agents
  getAgents() {
    return this.fetch<Agent[]>('/api/v1/agents');
  }

  getAgent(id: string) {
    return this.fetch<Agent>(`/api/v1/agents/${id}`);
  }

  // Permissions
  getPermissions(agentId: string) {
    return this.fetch<Permissions>(`/api/v1/agents/${agentId}/permissions`);
  }

  updatePermissions(agentId: string, scopes: string[]) {
    return this.fetch<void>(`/api/v1/agents/${agentId}/permissions`, {
      method: 'PUT',
      body: JSON.stringify({ scopes }),
    });
  }
}
```

---

## Build & Deployment

```bash
# Build
cd console
npm run build  # Outputs to console/dist/

# The AIPlex API serves the Console as static files
# src/aiplex/console/static/ ← copy of console/dist/
```

```python
# In main.py
from fastapi.staticfiles import StaticFiles

app.mount("/", StaticFiles(directory="console/static", html=True), name="console")
```

> Decision: Console is served by the AIPlex API pod, not a separate CDN or pod. This simplifies deployment (one pod serves both API and UI) and avoids CORS issues. For production scale, a CDN can be added in front.

---

## Responsive Design

The Console targets desktop browsers (admin dashboards). Minimum supported width: 1024px. Mobile is not a priority for v1.

---

## Accessibility

- All interactive elements have ARIA labels (provided by Shadcn/ui)
- Keyboard navigation for all forms and modals
- Color is never the sole indicator of state (StatusBadge includes icon + label)
- Screen reader friendly table layouts for instance lists

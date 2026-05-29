import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docs: [
    {
      type: 'doc',
      id: 'index',
      label: 'Welcome',
    },
    {
      type: 'category',
      label: 'Getting Started',
      collapsed: false,
      items: [
        'getting-started/quickstart',
        'getting-started/installation',
        'getting-started/first-deploy',
        'getting-started/connect-agent',
        'getting-started/platform-setup',
      ],
    },
    {
      type: 'category',
      label: 'Core Concepts',
      items: [
        'concepts/three-planes',
        'concepts/authentication',
        'concepts/scopes-and-permissions',
        'concepts/identity-zero-trust',
        'concepts/gateway-routing',
      ],
    },
    {
      type: 'category',
      label: 'Guides',
      items: [
        'guides/mcplex',
        'guides/a2aplex',
        'guides/llmplex',
        'guides/agents',
        'guides/permissions',
        'guides/catalog',
        'guides/observability',
        'guides/declarative-config',
        'guides/tape-runtime',
      ],
    },
    {
      type: 'category',
      label: 'Reference',
      items: [
        'reference/cli',
        'reference/configuration',
        'reference/sdk',
        'reference/opa-policy',
        'reference/envoy-routes',
      ],
    },
    {
      type: 'category',
      label: 'Architecture',
      items: [
        'architecture/overview',
        'architecture/request-flow',
        'architecture/auth-deep-dive',
        'architecture/deploy-engine',
        'architecture/data-model',
        'architecture/security-model',
        'architecture/performance',
        'architecture/embedded-tier-decision',
      ],
    },
    'contributing',
  ],
  api: [
    {
      type: 'doc',
      id: 'api/overview',
      label: 'Overview',
    },
    {
      type: 'category',
      label: 'Endpoints',
      items: [
        'api/catalog',
        'api/instances',
        'api/agents',
        'api/permissions',
        'api/llm-routes',
        'api/a2a',
        'api/dashboard',
        'api/auth',
      ],
    },
  ],
};

export default sidebars;

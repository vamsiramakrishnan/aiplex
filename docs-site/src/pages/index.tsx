import {Fragment, type ReactNode} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import CodeBlock from '@theme/CodeBlock';
import Heading from '@theme/Heading';
import McpIcon from '@site/src/brand/icons/mcplex.svg';
import A2aIcon from '@site/src/brand/icons/a2aplex.svg';
import LlmIcon from '@site/src/brand/icons/llmplex.svg';
import ClientIcon from '@site/src/brand/icons/clientplex.svg';
import styles from './index.module.css';

const journeys = [
  {
    name: 'Agents',
    module: 'A2APlex',
    guide: '/docs/guides/a2aplex',
    Icon: A2aIcon,
    detail: 'Publish an A2A agent or invoke a connected cloud agent.',
    operation: 'POST /api/v1/a2a/connections/{id}/invoke',
  },
  {
    name: 'Models',
    module: 'LLMPlex',
    guide: '/docs/guides/llmplex',
    Icon: LlmIcon,
    detail: 'Expose a model through its native API and see the usage the provider reports.',
    operation: 'POST /ap/{id}/v1/responses',
  },
  {
    name: 'Tools',
    module: 'MCPlex',
    guide: '/docs/guides/mcplex',
    Icon: McpIcon,
    detail: 'Connect an MCP server and choose which tools each caller can use.',
    operation: 'POST /ap/{id}/mcp',
  },
  {
    name: 'Harnesses',
    module: 'ClientPlex',
    guide: '/docs/guides/clientplex',
    Icon: ClientIcon,
    detail: 'Configure Codex, Claude Code or Gemini CLI to use your models and tools.',
    operation: 'aiplex connect local --client codex',
  },
];

function ProductGuides(): ReactNode {
  return (
    <nav aria-label="Product guides">
      <ul className={styles.journeys}>
        {journeys.map(({name, module, guide, Icon, detail, operation}) => (
          <li key={name}>
            <Link to={guide} className={styles.journey}>
              <span className={styles.module}>
                <Icon aria-hidden="true" focusable="false" />
                {module}
              </span>
              <span className={styles.label}>{name}<span aria-hidden="true">→</span></span>
              <span className={styles.detail}>{detail}</span>
              <code className={styles.operation}>
                {operation.split('/').map((part, index) => (
                  <Fragment key={index}>{index > 0 && <>/<wbr /></>}{part}</Fragment>
                ))}
              </code>
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}

export default function Home(): ReactNode {
  return (
    <Layout
      title="A control plane for agents, models, tools and harnesses"
      description="Publish agents, route model calls, connect MCP tools and configure coding harnesses. Share identity, policy and budgets across supported operations.">
      <main className={styles.home}>
        <header className={styles.hero}>
          <Heading as="h1">Connect agents, models, tools and harnesses.</Heading>
          <div className={styles.intro}>
            <p>Choose who can invoke an agent, which provider serves a model and which
              tools a coding harness can reach. Trace managed calls back to their callers.</p>
            <div className={styles.actions}>
              <Link className={styles.primary} to="/docs/getting-started/local-development">Run locally</Link>
              <Link to="/docs/concepts/request-path">Follow a request <span aria-hidden="true">→</span></Link>
            </div>
          </div>
        </header>

        <ProductGuides />

        <section className={styles.budget}>
          <div>
            <Heading as="h2">One budget for models and tools.</Heading>
            <p>Spend a USD 0.003 allowance on one mock model call and one MCP tool call.
              AIPlex blocks the next tool call. Run from a built checkout with Python 3 and curl.
              If a local stack is running, stop it first with <code>bin/aiplex dev stop</code>.</p>
            <Link to="/docs/guides/quotaplex">QuotaPlex walkthrough <span aria-hidden="true">→</span></Link>
          </div>
          <div className={styles.commands}>
            <CodeBlock title="From the repository root" language="bash">
              {'export PATH="$PWD/bin:$PATH"\naiplex dev --quota-tariff --detach\nbash examples/budgets/shared-spend.sh\naiplex dev stop'}
            </CodeBlock>
          </div>
        </section>
        <nav className={styles.shortcuts} aria-label="Operator guides">
          <Link to="/docs/library/model-providers">Add a provider</Link>
          <Link to="/docs/library/agent-identity-federation">Federate agent identity</Link>
          <Link to="/docs/library/usage-ledger">Inspect usage</Link>
          <Link to="/docs/library/quotaplex-execution-coverage">Check quota coverage</Link>
          <Link to="/docs/guides/skillsplex">Discover skills</Link>
        </nav>
      </main>
    </Layout>
  );
}

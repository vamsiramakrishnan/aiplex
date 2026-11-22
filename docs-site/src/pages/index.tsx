import type {ReactNode} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import CodeBlock from '@theme/CodeBlock';
import Heading from '@theme/Heading';
import AccessPointIcon from '@site/src/brand/icons/access-point.svg';
import McpIcon from '@site/src/brand/icons/mcplex.svg';
import A2aIcon from '@site/src/brand/icons/a2aplex.svg';
import LlmIcon from '@site/src/brand/icons/llmplex.svg';
import QuotaIcon from '@site/src/brand/icons/quotaplex.svg';
import ClientIcon from '@site/src/brand/icons/clientplex.svg';
import SkillsIcon from '@site/src/brand/icons/skillsplex.svg';
import styles from './index.module.css';

const gateways = [
  {name: 'LLMPlex', id: 'llmplex', Icon: LlmIcon, title: 'Route model calls', detail: 'Connect a provider, expose a model and inspect its usage.'},
  {name: 'MCPlex', id: 'mcplex', Icon: McpIcon, title: 'Publish tools', detail: 'Register an MCP server and choose which tools a caller can run.'},
  {name: 'A2APlex', id: 'a2aplex', Icon: A2aIcon, title: 'Delegate to agents', detail: 'Deploy an A2A agent or invoke a connected cloud agent.'},
];

const controls = [
  {name: 'QuotaPlex', id: 'quotaplex', Icon: QuotaIcon, title: 'Enforce budgets', detail: 'Reserve budget before dispatch and settle from verified usage.'},
  {name: 'ClientPlex', id: 'clientplex', Icon: ClientIcon, title: 'Connect developer clients', detail: 'Generate configuration for a supported IDE or coding agent.'},
  {name: 'SkillsPlex', id: 'skillsplex', Icon: SkillsIcon, title: 'Discover skills', detail: 'Publish manifests and inspect reported skill invocations.'},
];

function GuideList({title, items}: {title: string; items: typeof gateways}): ReactNode {
  return (
    <section className={styles.guideList}>
      <Heading as="h3">{title}</Heading>
      <ul>
        {items.map(({name, id, Icon, title, detail}) => (
          <li key={id}>
            <Link to={`/docs/guides/${id}`} className={styles.guide}>
              <Icon className={styles.guideIcon} aria-hidden="true" focusable="false" />
              <span>
                <span className={styles.moduleName}>{name}</span>
                <strong>{title}</strong>
                <span className={styles.detail}>{detail}</span>
              </span>
              <span className={styles.arrow} aria-hidden="true">→</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

function AccessPoint(): ReactNode {
  return (
    <figure className={styles.diagram}>
      <figcaption>Access point routes</figcaption>
      <div className={styles.diagramContents}>
        <div className={styles.callers}>Agents, people and developer clients</div>
        <div className={styles.gate}>
          <AccessPointIcon aria-hidden="true" focusable="false" />
          <div><code>/ap/{'{id}'}</code><span>Verify caller and select resources</span></div>
        </div>
        <ul className={styles.routes}>
          <li><span>Model call</span><code>/v1/responses</code></li>
          <li><span>MCP tools</span><code>/mcp</code></li>
          <li><span>Skill discovery</span><code>/skills</code></li>
        </ul>
      </div>
    </figure>
  );
}

export default function Home(): ReactNode {
  return (
    <Layout
      title="A control plane for AI access"
      description="Give teams an access point for AI models, tools and skills. Connect clients, enforce budgets and inspect managed usage.">
      <main className={styles.home}>
        <header className={styles.hero}>
          <div className={styles.intro}>
            <Heading as="h1">One access point for models and tools.</Heading>
            <p>Give each team a stable URL. Choose which models, tools and skills its callers
              can reach. Trace each managed call back to its caller.</p>
            <div className={styles.actions}>
              <Link className={styles.primary} to="/docs/getting-started/local-development">Run locally</Link>
              <Link to="/docs/concepts/request-path">Follow a request <span aria-hidden="true">→</span></Link>
            </div>
          </div>
          <AccessPoint />
        </header>

        <section className={styles.guides}>
          <div className={styles.sectionHead}>
            <Heading as="h2">Control plane guides</Heading>
            <Link to="/docs/guides">All guides <span aria-hidden="true">→</span></Link>
          </div>
          <div className={styles.columns}>
            <GuideList title="Traffic gateways" items={gateways} />
            <GuideList title="Budgets, clients and discovery" items={controls} />
          </div>
        </section>

        <section className={styles.budget}>
          <div>
            <Heading as="h2">One budget for models and tools.</Heading>
            <p>Spend a USD 0.003 allowance on one mock model call and one MCP tool call.
              See the next request refused. Run from a built checkout with Python 3 and curl.</p>
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
        </nav>
      </main>
    </Layout>
  );
}

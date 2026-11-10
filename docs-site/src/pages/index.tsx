import type {ReactNode} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

const entryLinks = [
  {
    to: '/docs/guides/clientplex',
    title: 'Connect an IDE',
    text: 'Point Claude, Codex or Antigravity at an access point with a reviewed configuration.',
  },
  {
    to: '/docs/library/model-providers',
    title: 'Add a model provider',
    text: 'OpenAI, Anthropic, Azure OpenAI, Bedrock, Gemini and OpenAI-compatible endpoints.',
  },
  {
    to: '/docs/library/agent-identity-federation',
    title: 'Federate agent identity',
    text: 'Let a workload authenticate as a registered agent with its own cloud token.',
  },
  {
    to: '/docs/library/usage-ledger',
    title: 'See who used what',
    text: 'One record per managed exchange: caller, model or tool, tokens and cost.',
  },
  {
    to: '/docs/getting-started',
    title: 'Run it locally',
    text: 'Build the CLI and API and start the local development stack.',
  },
  {
    to: '/docs/guides/quotaplex',
    title: 'Share a budget',
    text: 'Opt-in allocations, admission and settlement on covered execution paths.',
  },
];

function Diagram(): ReactNode {
  const box = {fill: 'var(--aiplex-diagram-box)', stroke: 'var(--aiplex-diagram-line)'};
  const accent = {fill: 'var(--aiplex-diagram-box)', stroke: 'var(--aiplex-diagram-accent)'};
  const text = {fill: 'var(--aiplex-diagram-text)', fontFamily: 'var(--ifm-font-family-base)'};
  const line = {stroke: 'var(--aiplex-diagram-line)', strokeWidth: 1.5, fill: 'none'};
  return (
    <svg
      viewBox="0 0 900 300"
      role="img"
      aria-labelledby="aiplex-diagram-title aiplex-diagram-desc">
      <title id="aiplex-diagram-title">Callers, access point, resources</title>
      <desc id="aiplex-diagram-desc">
        Agents, users and clients reach an access point at /ap/id. The access point checks
        identity, admission and quota and writes the usage ledger. It fronts models, MCP
        tools and skills.
      </desc>
      <defs>
        <marker id="aiplex-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M0 0 L10 5 L0 10 z" style={{fill: 'var(--aiplex-diagram-line)'}} />
        </marker>
      </defs>

      <text x="95" y="28" textAnchor="middle" fontSize="14" fontWeight="700" style={text}>Callers</text>
      {['Agents', 'Users', 'Clients and IDEs'].map((label, i) => (
        <g key={label}>
          <rect x="15" y={50 + i * 75} width="160" height="55" rx="6" strokeWidth="1.5" style={box} />
          <text x="95" y={83 + i * 75} textAnchor="middle" fontSize="15" style={text}>{label}</text>
          <path d={`M175 ${77 + i * 75} L335 ${150}`} markerEnd="url(#aiplex-arrow)" style={line} />
        </g>
      ))}

      <text x="450" y="28" textAnchor="middle" fontSize="14" fontWeight="700" style={text}>Access point</text>
      <rect x="340" y="45" width="220" height="215" rx="8" strokeWidth="2" style={accent} />
      <text x="450" y="78" textAnchor="middle" fontSize="15" fontWeight="700" style={text}>/ap/{'{id}'}/…</text>
      {['Who is calling', 'Is this kind of caller allowed', 'QoS and quota controls', 'Usage ledger record'].map(
        (label, i) => (
          <text key={label} x="450" y={115 + i * 34} textAnchor="middle" fontSize="14" style={text}>{label}</text>
        ),
      )}

      <text x="785" y="28" textAnchor="middle" fontSize="14" fontWeight="700" style={text}>Resources</text>
      {['Models', 'MCP tools', 'Skills'].map((label, i) => (
        <g key={label}>
          <path d={`M560 150 L725 ${77 + i * 75}`} markerEnd="url(#aiplex-arrow)" style={line} />
          <rect x="725" y={50 + i * 75} width="160" height="55" rx="6" strokeWidth="1.5" style={box} />
          <text x="805" y={83 + i * 75} textAnchor="middle" fontSize="15" style={text}>{label}</text>
        </g>
      ))}
    </svg>
  );
}

export default function Home(): ReactNode {
  return (
    <Layout
      title="Control plane for agent access"
      description="AIPlex puts MCP, A2A and model traffic behind one gateway, one auth stack, one policy layer and one audit trail.">
      <main className="aiplex-home">
        <Heading as="h1">AIPlex</Heading>
        <p className="aiplex-lede">
          AIPlex is a control plane for agent interactions. It puts MCP, A2A and model traffic
          behind one gateway, one auth stack, one policy layer and one audit trail.
        </p>

        <div className="aiplex-diagram">
          <Diagram />
        </div>

        <Heading as="h2">Start with a task</Heading>
        <div className="aiplex-links">
          {entryLinks.map((l) => (
            <Link key={l.to} to={l.to} className="aiplex-link-card">
              <strong>{l.title}</strong>
              <span>{l.text}</span>
            </Link>
          ))}
        </div>

        <Heading as="h2">The modules</Heading>
        <table className="aiplex-modules">
          <thead>
            <tr>
              <th>Module</th>
              <th>What it governs</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td><Link to="/docs/guides/mcplex">MCPlex</Link></td>
              <td>Agent to tool: MCP connections, upstream auth, tool policy</td>
            </tr>
            <tr>
              <td><Link to="/docs/guides/llmplex">LLMPlex</Link></td>
              <td>Agent to model: providers, surfaces, metering, batches, capacity</td>
            </tr>
            <tr>
              <td><Link to="/docs/guides/a2aplex">A2APlex</Link></td>
              <td>Agent to agent: native cloud agents, delegation history</td>
            </tr>
            <tr>
              <td><Link to="/docs/guides/quotaplex">QuotaPlex</Link></td>
              <td>Shared budgets and admission (opt-in)</td>
            </tr>
            <tr>
              <td><Link to="/docs/guides/clientplex">ClientPlex</Link></td>
              <td>Developer client configuration plans</td>
            </tr>
            <tr>
              <td><Link to="/docs/guides/skillsplex">SkillsPlex</Link></td>
              <td>Skill discovery and reported invocation history</td>
            </tr>
          </tbody>
        </table>
      </main>
    </Layout>
  );
}

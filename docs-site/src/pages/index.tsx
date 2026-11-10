import type {ReactNode, SVGProps, ComponentType} from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import AgentIcon from '@site/src/brand/icons/agent.svg';
import UserIcon from '@site/src/brand/icons/user.svg';
import ClientIcon from '@site/src/brand/icons/client.svg';
import AccessPointIcon from '@site/src/brand/icons/access-point.svg';
import McpIcon from '@site/src/brand/icons/mcplex.svg';
import A2aIcon from '@site/src/brand/icons/a2aplex.svg';
import LlmIcon from '@site/src/brand/icons/llmplex.svg';
import QuotaIcon from '@site/src/brand/icons/quotaplex.svg';
import ClientPlexIcon from '@site/src/brand/icons/clientplex.svg';
import SkillsIcon from '@site/src/brand/icons/skillsplex.svg';

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

type IconProps = SVGProps<SVGSVGElement>;

function Diagram(): ReactNode {
  const box = {fill: 'var(--aiplex-diagram-box)', stroke: 'var(--aiplex-diagram-line)'};
  const accent = {fill: 'var(--aiplex-diagram-box)', stroke: 'var(--aiplex-diagram-accent)'};
  const text = {fill: 'var(--aiplex-diagram-text)', fontFamily: 'var(--ifm-font-family-base)'};
  const line = {stroke: 'var(--aiplex-diagram-line)', strokeWidth: 1.5, fill: 'none'};
  const callers: [string, ComponentType<IconProps>][] = [
    ['Agents', AgentIcon],
    ['Users', UserIcon],
    ['Clients', ClientIcon],
  ];
  const resources: [string, string, string, ComponentType<IconProps>][] = [
    ['LLMPlex', 'Models', 'var(--aiplex-module-llmplex)', LlmIcon],
    ['MCPlex', 'MCP tools', 'var(--aiplex-module-mcplex)', McpIcon],
    ['SkillsPlex', 'Skills', 'var(--aiplex-module-skillsplex)', SkillsIcon],
  ];
  return (
    <svg
      viewBox="0 0 900 300"
      role="img"
      aria-labelledby="aiplex-diagram-title aiplex-diagram-desc">
      <title id="aiplex-diagram-title">Callers, access point, resources</title>
      <desc id="aiplex-diagram-desc">
        Agents, users and clients (including IDEs) reach an access point at /ap/id. The access point checks
        identity, admission and quota and writes the usage ledger. It fronts models, MCP
        tools and skills.
      </desc>
      <defs>
        <marker id="aiplex-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M0 0 L10 5 L0 10 z" style={{fill: 'var(--aiplex-diagram-line)'}} />
        </marker>
      </defs>

      <text x="95" y="28" textAnchor="middle" fontSize="14" fontWeight="700" style={text}>Callers</text>
      {callers.map(([label, Icon], i) => (
        <g key={label}>
          <rect x="15" y={50 + i * 75} width="160" height="55" rx="6" strokeWidth="1.5" style={box} />
          <Icon x="27" y={65 + i * 75} width="24" height="24" style={{color: 'var(--aiplex-diagram-text)'}} aria-hidden="true" />
          <text x="60" y={83 + i * 75} fontSize="15" style={text}>{label}</text>
          <path d={`M175 ${77 + i * 75} L335 150`} markerEnd="url(#aiplex-arrow)" style={line} />
        </g>
      ))}

      <text x="450" y="28" textAnchor="middle" fontSize="14" fontWeight="700" style={text}>Access point</text>
      <rect x="340" y="45" width="220" height="215" rx="8" strokeWidth="2" style={accent} />
      <AccessPointIcon x="438" y="58" width="24" height="24" style={{color: 'var(--aiplex-diagram-text)'}} aria-hidden="true" />
      <text x="450" y="106" textAnchor="middle" fontSize="15" fontWeight="700" style={text}>/ap/{'{id}'}/…</text>
      {['Who is calling', 'Is this kind of caller allowed', 'QoS and quota controls', 'Usage ledger record'].map(
        (label, i) => (
          <text key={label} x="450" y={140 + i * 28} textAnchor="middle" fontSize="14" style={text}>{label}</text>
        ),
      )}

      <text x="785" y="28" textAnchor="middle" fontSize="14" fontWeight="700" style={text}>Resources</text>
      {resources.map(([module, what, color, Icon], i) => (
        <g key={module}>
          <path d={`M560 150 L725 ${77 + i * 75}`} markerEnd="url(#aiplex-arrow)" style={line} />
          <rect x="725" y={50 + i * 75} width="160" height="55" rx="6" strokeWidth="1.5" style={box} />
          <Icon x="737" y={65 + i * 75} width="24" height="24" style={{color}} aria-hidden="true" />
          <text x="771" y={73 + i * 75} fontSize="15" fontWeight="700" style={text}>{module}</text>
          <text x="771" y={93 + i * 75} fontSize="13" style={text}>{what}</text>
        </g>
      ))}
    </svg>
  );
}

const moduleRows: [string, string, string, ComponentType<IconProps>, string][] = [
  ['MCPlex', '/docs/guides/mcplex', 'mcplex', McpIcon, 'Agent to tool: MCP connections, upstream auth, tool policy'],
  ['LLMPlex', '/docs/guides/llmplex', 'llmplex', LlmIcon, 'Agent to model: providers, surfaces, metering, batches, capacity'],
  ['A2APlex', '/docs/guides/a2aplex', 'a2aplex', A2aIcon, 'Agent to agent: native cloud agents, delegation history'],
  ['QuotaPlex', '/docs/guides/quotaplex', 'quotaplex', QuotaIcon, 'Shared budgets and admission (opt-in)'],
  ['ClientPlex', '/docs/guides/clientplex', 'clientplex', ClientPlexIcon, 'Developer client configuration plans'],
  ['SkillsPlex', '/docs/guides/skillsplex', 'skillsplex', SkillsIcon, 'Skill discovery and reported invocation history'],
];

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
            {moduleRows.map(([name, to, key, Icon, what]) => (
              <tr key={name}>
                <td>
                  <span className="aiplex-module-name">
                    <Icon
                      width="20"
                      height="20"
                      aria-hidden="true"
                      style={{color: `var(--aiplex-module-${key})`}}
                    />
                    <Link to={to}>{name}</Link>
                  </span>
                </td>
                <td>{what}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </main>
    </Layout>
  );
}

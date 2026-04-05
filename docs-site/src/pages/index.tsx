import type {ReactNode} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

function HeroSection() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--aiplex', styles.heroBanner)}>
      <div className="container">
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className="hero__subtitle">
          Unified control plane for AI agent interactions.
          Govern tools, agents, and models through a single gateway.
        </p>
        <div className={styles.buttons}>
          <Link className="button button--primary button--lg" to="/docs/getting-started/quickstart">
            Get Started in 60 Seconds
          </Link>
          <Link className="button button--outline button--lg" style={{marginLeft: '1rem', color: 'inherit', borderColor: 'currentColor'}} to="/docs">
            Read the Docs
          </Link>
        </div>
      </div>
    </header>
  );
}

function ThreePlanes() {
  return (
    <section className="container" style={{padding: '4rem 0'}}>
      <div style={{textAlign: 'center', marginBottom: '2rem'}}>
        <Heading as="h2">Three Planes, One Gateway</Heading>
        <p style={{maxWidth: 600, margin: '0 auto', opacity: 0.8}}>
          AIPlex governs every AI interaction through three purpose-built planes,
          unified by a single auth stack, policy engine, and audit trail.
        </p>
      </div>
      <div className="plane-cards">
        <Link to="/docs/guides/mcplex" className="plane-card" style={{textDecoration: 'none', color: 'inherit'}}>
          <span className="plane-tag plane-tag--mcp">MCPlex</span>
          <h3>Agent &#8596; Tool</h3>
          <p>Deploy and govern MCP servers. Agents call tools like <code>search_curriculum</code> or <code>generate_quiz</code> through scoped, audited access.</p>
          <code style={{fontSize: '0.8rem'}}>mcp:tools:search_curriculum</code>
        </Link>
        <Link to="/docs/guides/a2aplex" className="plane-card" style={{textDecoration: 'none', color: 'inherit'}}>
          <span className="plane-tag plane-tag--a2a">A2APlex</span>
          <h3>Agent &#8596; Agent</h3>
          <p>Orchestrate agent delegation. A tutor agent delegates research to a research agent, with identity and consent at every hop.</p>
          <code style={{fontSize: '0.8rem'}}>a2a:task:research</code>
        </Link>
        <Link to="/docs/guides/llmplex" className="plane-card" style={{textDecoration: 'none', color: 'inherit'}}>
          <span className="plane-tag plane-tag--llm">LLMPlex</span>
          <h3>Agent &#8596; Model</h3>
          <p>Route model inference with failover, cost budgets, and per-agent access control. Gemini, Claude, GPT, Bedrock, Ollama.</p>
          <code style={{fontSize: '0.8rem'}}>llm:model:gemini-2.5-flash</code>
        </Link>
      </div>
    </section>
  );
}

function QuickStart() {
  return (
    <section style={{background: 'var(--ifm-background-surface-color)', padding: '4rem 0'}}>
      <div className="container">
        <div style={{textAlign: 'center', marginBottom: '2rem'}}>
          <Heading as="h2">Deploy Your First Tool in 60 Seconds</Heading>
        </div>
        <div className="quickstart-steps">
          <div className="quickstart-step">
            <div className="step-number">1</div>
            <h4>Install</h4>
            <code>curl -fsSL https://get.aiplex.dev | sh</code>
          </div>
          <div className="quickstart-step">
            <div className="step-number">2</div>
            <h4>Login</h4>
            <code>aiplex login</code>
          </div>
          <div className="quickstart-step">
            <div className="step-number">3</div>
            <h4>Deploy</h4>
            <code>aiplex deploy</code>
          </div>
          <div className="quickstart-step">
            <div className="step-number">4</div>
            <h4>Use</h4>
            <code>aiplex status my-tool</code>
          </div>
        </div>
        <div style={{textAlign: 'center', marginTop: '1.5rem'}}>
          <Link className="button button--primary" to="/docs/getting-started/quickstart">
            Follow the Full Quickstart
          </Link>
        </div>
      </div>
    </section>
  );
}

function WhyAIPlex() {
  const features = [
    {
      title: 'Zero Infrastructure Vocabulary',
      description: 'You think in tools, agents, and models. Not SPIFFE, Envoy, or OPA. AIPlex hides the complexity.',
    },
    {
      title: 'One Token, All Planes',
      description: 'A single JWT carries scopes across MCPlex, A2APlex, and LLMPlex. No cross-service token exchange.',
    },
    {
      title: 'Defense in Depth',
      description: 'mTLS, per-workload SPIFFE identity, OPA policy enforcement, and three-dimensional consent (agent ceiling, user ceiling, delegation).',
    },
    {
      title: 'Progressive Disclosure',
      description: 'Start with zero-config interactive CLI. Graduate to YAML. Escape to raw K8s when you need it.',
    },
    {
      title: 'Federated Catalog',
      description: 'Browse the Official MCP Registry, MACH Alliance, Google 1P servers, and your own private templates in one place.',
    },
    {
      title: 'Full Audit Trail',
      description: 'Every tool call, agent delegation, and model request is traced with both user and agent identity.',
    },
  ];

  return (
    <section className="container" style={{padding: '4rem 0'}}>
      <div style={{textAlign: 'center', marginBottom: '2rem'}}>
        <Heading as="h2">Why AIPlex</Heading>
      </div>
      <div style={{display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(300px, 1fr))', gap: '2rem'}}>
        {features.map((f, i) => (
          <div key={i} style={{padding: '1rem 0'}}>
            <h4 style={{marginBottom: '0.5rem'}}>{f.title}</h4>
            <p style={{opacity: 0.8, margin: 0}}>{f.description}</p>
          </div>
        ))}
      </div>
    </section>
  );
}

function CTASection() {
  return (
    <section style={{
      background: 'linear-gradient(135deg, var(--ifm-color-primary-darkest), var(--ifm-color-primary-dark))',
      padding: '4rem 0',
      textAlign: 'center',
      color: 'white',
    }}>
      <div className="container">
        <Heading as="h2" style={{color: 'white'}}>Ready to Govern Your AI Agents?</Heading>
        <p style={{maxWidth: 500, margin: '0 auto 1.5rem', opacity: 0.9}}>
          From a single MCP tool to a full multi-agent orchestration platform.
          Start where you are, grow as you need.
        </p>
        <Link className="button button--secondary button--lg" to="/docs/getting-started/quickstart">
          Get Started
        </Link>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  return (
    <Layout
      title="Unified AI Agent Control Plane"
      description="AIPlex - Unified control plane for AI agent interactions. Govern tools (MCPlex), agents (A2APlex), and models (LLMPlex) through a single gateway.">
      <HeroSection />
      <main>
        <ThreePlanes />
        <QuickStart />
        <WhyAIPlex />
        <CTASection />
      </main>
    </Layout>
  );
}

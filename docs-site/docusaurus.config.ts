import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';
import remarkRepoLinks from './plugins/remark-repo-links.mjs';

const REPO = 'https://github.com/vamsiramakrishnan/aiplex';

// The documentation address, in one place. The site deploys to GitHub Pages
// (.github/workflows/deploy-docs.yml). Nothing in the repository shows that a
// custom domain exists (no CNAME file, no DNS notes). If one is added later,
// change these two constants (for example origin 'https://docs.example.com',
// base '/'), add static/CNAME, and update the link block at the bottom of
// README.md, which is the only other place that spells the address out.
const DOCS_ORIGIN = 'https://vamsiramakrishnan.github.io';
const DOCS_BASE_URL = '/aiplex/';

const config: Config = {
  title: 'AIPlex',
  tagline: 'One control plane for agent access to models, tools, skills and other agents.',
  favicon: 'img/favicon.svg',

  future: {
    v4: true,
  },

  url: DOCS_ORIGIN,
  baseUrl: DOCS_BASE_URL,

  organizationName: 'vamsiramakrishnan',
  projectName: 'aiplex',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  markdown: {
    // .md files are plain CommonMark, .mdx files are MDX. The verified docs synced from
    // the repository's docs/ directory contain `<`, `{` and angle-bracket placeholders
    // that MDX would reject, and they must not be rewritten.
    format: 'detect',
    hooks: {
      onBrokenMarkdownLinks: 'throw',
      onBrokenMarkdownImages: 'throw',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl: `${REPO}/tree/main/docs-site/`,
          showLastUpdateTime: false,
          // Rewrites links from the synced docs/ files to repository source files.
          beforeDefaultRemarkPlugins: [remarkRepoLinks],
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/aiplex-social-card.png',
    colorMode: {
      defaultMode: 'light',
      respectPrefersColorScheme: true,
    },
    navbar: {
      logo: {
        alt: 'AIPlex',
        src: 'img/aiplex-logo.svg',
        srcDark: 'img/aiplex-logo-dark.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docs',
          position: 'left',
          label: 'Docs',
        },
        {
          type: 'doc',
          docId: 'getting-started/index',
          position: 'left',
          label: 'Get started',
        },
        {
          type: 'doc',
          docId: 'reference/cli/index',
          position: 'left',
          label: 'CLI',
        },
        {
          type: 'doc',
          docId: 'api/overview',
          position: 'left',
          label: 'API',
        },
        {
          href: REPO,
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'light',
      links: [
        {
          title: 'Start',
          items: [
            {label: 'Overview', to: '/docs'},
            {label: 'Getting started', to: '/docs/getting-started'},
            {label: 'Concepts', to: '/docs/concepts'},
          ],
        },
        {
          title: 'Modules',
          items: [
            {label: 'MCPlex (tools)', to: '/docs/guides/mcplex'},
            {label: 'LLMPlex (models)', to: '/docs/guides/llmplex'},
            {label: 'A2APlex (agents)', to: '/docs/guides/a2aplex'},
            {label: 'QuotaPlex', to: '/docs/guides/quotaplex'},
          ],
        },
        {
          title: 'Reference',
          items: [
            {label: 'CLI', to: '/docs/reference/cli'},
            {label: 'API', to: '/docs/api/overview'},
            {label: 'Usage-format coverage', to: '/docs/library/reference/usage-formats/coverage'},
          ],
        },
        {
          title: 'Project',
          items: [
            {label: 'GitHub', href: REPO},
            {label: 'Contributing', to: '/docs/contributing'},
          ],
        },
      ],
      copyright: `Copyright ${new Date().getFullYear()} AIPlex Contributors. Built with Docusaurus.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'go', 'rust', 'rego', 'yaml', 'json', 'toml', 'protobuf'],
    },
    tableOfContents: {
      minHeadingLevel: 2,
      maxHeadingLevel: 4,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;

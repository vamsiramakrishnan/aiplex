import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'AIPlex',
  tagline: 'One gateway. Three planes. Governed AI agents.',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  // For custom domain: url: 'https://docs.aiplex.dev', baseUrl: '/'
  // For GitHub Pages without custom domain:
  url: 'https://vamsiramakrishnan.github.io',
  baseUrl: '/aiplex/',

  organizationName: 'vamsiramakrishnan',
  projectName: 'aiplex',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'warn',
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
          editUrl: 'https://github.com/vamsiramakrishnan/aiplex/tree/main/docs-site/',
          showLastUpdateTime: true,
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
      defaultMode: 'dark',
      respectPrefersColorScheme: true,
    },
    announcementBar: {
      id: 'alpha',
      content: 'AIPlex is in active development. <a href="/docs/contributing">Contributions welcome!</a>',
      isCloseable: true,
    },
    navbar: {
      title: 'AIPlex',
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docs',
          position: 'left',
          label: 'Docs',
        },
        {
          type: 'docSidebar',
          sidebarId: 'api',
          position: 'left',
          label: 'API Reference',
        },
        {
          href: 'https://github.com/vamsiramakrishnan/aiplex',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Get Started',
          items: [
            {label: 'Quickstart', to: '/docs/getting-started/quickstart'},
            {label: 'Installation', to: '/docs/getting-started/installation'},
            {label: 'Your First Deploy', to: '/docs/getting-started/first-deploy'},
          ],
        },
        {
          title: 'Planes',
          items: [
            {label: 'MCPlex (Tools)', to: '/docs/guides/mcplex'},
            {label: 'A2APlex (Agents)', to: '/docs/guides/a2aplex'},
            {label: 'LLMPlex (Models)', to: '/docs/guides/llmplex'},
          ],
        },
        {
          title: 'Reference',
          items: [
            {label: 'CLI', to: '/docs/reference/cli'},
            {label: 'API', to: '/docs/api/overview'},
            {label: 'Configuration', to: '/docs/reference/configuration'},
          ],
        },
        {
          title: 'Community',
          items: [
            {label: 'GitHub', href: 'https://github.com/vamsiramakrishnan/aiplex'},
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

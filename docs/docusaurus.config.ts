import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'Warehouse Ops Agent',
  tagline:
    'The read-side decision-support agent that correlates the fleet\u2019s five bounded contexts into one ranked, human-gated recommendation \u2014 recommendations-only v1.',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  url: 'https://iqvo.github.io',
  baseUrl: '/warehouse-ops-agent/',

  organizationName: 'IQVO',
  projectName: 'warehouse-ops-agent',
  deploymentBranch: 'gh-pages',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'throw',
      onBrokenMarkdownImages: 'throw',
    },
  },

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          routeBasePath: 'docs',
          editUrl:
            'https://github.com/IQVO/warehouse-ops-agent/tree/main/docs/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themes: ['@docusaurus/theme-mermaid'],

  themeConfig: {
    image: 'img/logo.svg',
    colorMode: {
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'Warehouse Ops Agent',
      logo: {
        alt: 'Warehouse Ops Agent',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Documentation',
        },
        {
          to: '/docs/ecosystem/context-map',
          label: 'Context map',
          position: 'left',
        },
        {
          to: '/docs/adr',
          label: 'ADR',
          position: 'left',
        },
        {
          href: 'https://github.com/IQVO/warehouse-ops-agent',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Documentation',
          items: [
            {label: 'Overview', to: '/docs/overview'},
            {label: 'Business Context', to: '/docs/business-context/domain-vision'},
            {label: 'Domain-Driven Design', to: '/docs/ddd/subdomain-classification'},
            {label: 'Governance', to: '/docs/mcp/governance-note'},
          ],
        },
        {
          title: 'Ecosystem',
          items: [
            {label: 'Context map', to: '/docs/ecosystem/context-map'},
            {
              label: 'inventory-storage',
              href: 'https://github.com/IQVO/inventory-storage',
            },
            {
              label: 'wes-work-planning',
              href: 'https://github.com/IQVO/wes-work-planning',
            },
            {
              label: 'fulfillment-execution',
              href: 'https://github.com/IQVO/fulfillment-execution',
            },
            {
              label: 'workforce-management',
              href: 'https://github.com/IQVO/workforce-management',
            },
            {
              label: 'facility-layout',
              href: 'https://github.com/IQVO/facility-layout',
            },
          ],
        },
        {
          title: 'Source',
          items: [
            {
              label: 'warehouse-ops-agent on GitHub',
              href: 'https://github.com/IQVO/warehouse-ops-agent',
            },
          ],
        },
      ],
      copyright: `warehouse-systems \u00b7 warehouse-ops-agent \u2014 read-side decision-support, Customer of five Open Host Services. Built ${new Date().getFullYear()}.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'json', 'go', 'yaml'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;

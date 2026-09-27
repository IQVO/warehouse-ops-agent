import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

/**
 * The same shared top-level shape every warehouse-systems documentation
 * site uses: Overview, Business Context, Domain-Driven Design, Ecosystem,
 * AI Ecosystem (MCP), Architecture Decision Records. No API Reference
 * category here \u2014 this agent has no OpenAPI spec of its own yet (its two
 * inbound surfaces, GET /daily-brief and its MCP tools, are documented in
 * prose, see docs/api-surface.md).
 */
const sidebars: SidebarsConfig = {
  docsSidebar: [
    {
      type: 'category',
      label: 'Overview',
      collapsed: false,
      link: {type: 'doc', id: 'overview/index'},
      items: ['overview/getting-started'],
    },
    {
      type: 'category',
      label: 'Business Context',
      collapsed: false,
      items: [
        'business-context/domain-vision',
        'business-context/ubiquitous-language',
      ],
    },
    {
      type: 'category',
      label: 'Domain-Driven Design',
      collapsed: false,
      items: ['ddd/subdomain-classification'],
    },
    {
      type: 'category',
      label: 'API Surface',
      collapsed: false,
      items: ['api-surface'],
    },
    {
      type: 'category',
      label: 'Ecosystem',
      collapsed: false,
      items: ['ecosystem/context-map'],
    },
    {
      type: 'category',
      label: 'AI Ecosystem (MCP)',
      collapsed: false,
      items: ['mcp/governance-note'],
    },
    {
      type: 'category',
      label: 'Architecture Decision Records',
      collapsed: false,
      link: {type: 'doc', id: 'adr/index'},
      items: [
        'adr/0001-warehouse-ops-agent-placement',
        'adr/0002-micro-frontend-console-architecture',
        'adr/0003-console-bff-report-dashboards',
        'adr/0004-llm-reasoner-behind-the-policy-layer',
        'adr/0005-rest-identity-static-bearer-scopes',
        'adr/0006-fleet-wide-auth-removal',
        'adr/0007-second-wave-outbound-mcp-clients',
        'adr/0008-labor-utilization-advisory-correlation',
        'adr/0009-explain-travel-factor',
        'adr/0010-standard-metrics-convention',
        'adr/0011-reasoner-path-circuit-breaker-timeout-retry',
      ],
    },
  ],
};

export default sidebars;

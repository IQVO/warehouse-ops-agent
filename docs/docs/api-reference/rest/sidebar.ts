import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/warehouse-ops-agent-rest-console",
    },
    {
      type: "category",
      label: "health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-healthz",
          label: "Liveness probe.",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "briefs",
      link: {
        type: "doc",
        id: "api-reference/rest/briefs",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-daily-brief",
          label: "The shift-start operational brief.",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-flow-balance-exception",
          label: "Correlated staffing/flow advisory for one process path.",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-explain-travel-factor",
          label: "Explain the travel distance factor between two locations.",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-runtime-signals",
          label: "Phase 5 runtime-feedback signals.",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "console",
      link: {
        type: "doc",
        id: "api-reference/rest/console",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-order-lifecycle",
          label: "Order lifecycle read model for the console shell.",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-wms-dashboard",
          label: "WMS chart dashboard (order funnel, flow accuracy, catalog growth).",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-wes-dashboard",
          label: "WES chart dashboard (queue status, stuck tasks, station standards, labor).",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "network",
      link: {
        type: "doc",
        id: "api-reference/rest/network",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-master-data-gaps",
          label: "Product-master gap report (ADR 0020).",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-inbound-outlook",
          label: "Inbound-receiving outlook (ADR 0021).",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-stuck-transfers",
          label: "Stuck network transfers (ADR 0019).",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-transfer-status",
          label: "One transfer's status.",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-network-imbalance",
          label: "Network inventory imbalance view.",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "act",
      link: {
        type: "doc",
        id: "api-reference/rest/act",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/post-release-proposal",
          label: "Propose the release of the next work unit (ADR 0027).",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/post-release-approval",
          label: "Confirm and execute a proposal with its token (ADR 0027).",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/post-reallocation-proposal",
          label: "Propose labor reallocation moves (ADR 0029).",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/post-reallocation-approval",
          label: "Approve one move and delegate assign_labor (ADR 0029).",
          className: "api-method post",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;

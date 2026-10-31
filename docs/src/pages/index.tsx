import type {ReactNode} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';
import Heading from '@theme/Heading';

import styles from './index.module.css';

function StudyDisclaimer() {
  return (
    <div
      style={{
        background: '#fef3c7',
        color: '#78350f',
        textAlign: 'center',
        padding: '0.6rem 1rem',
        fontSize: '0.9rem',
        borderBottom: '1px solid #f59e0b',
      }}>
      ⚠️ <strong>Study project</strong> — an educational DDD exercise
      following real industry-standard MCP / agentic patterns. Not a
      production system. Not affiliated with, endorsed by, or
      representative of large real-world Blue Yonder, or any other company.
    </div>
  );
}

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero', styles.heroBanner)}>
      <StudyDisclaimer />
      <div className="container">
        <p className={styles.eyebrow}>
          warehouse-systems · read-side decision-support
        </p>
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className={clsx('hero__subtitle', styles.subtitle)}>
          {siteConfig.tagline}
        </p>

        <div className={styles.buttons}>
          <Link
            className="button button--primary button--lg"
            to="/docs/overview">
            Read the documentation
          </Link>
          <Link
            className="button button--secondary button--lg"
            to="/docs/api-surface">
            API surface
          </Link>
          <Link
            className="button button--secondary button--lg"
            to="/docs/ecosystem/context-map">
            Context map
          </Link>
        </div>
      </div>
    </header>
  );
}

function WhatItOwns() {
  return (
    <section className={styles.ownership}>
      <div className="container">
        <div className="row">
          <div className="col col--6">
            <Heading as="h2" className={styles.ownsHeading}>
              What it owns
            </Heading>
            <ul className={styles.ownsList}>
              <li>Correlating signals from five contexts into one decision</li>
              <li>The full evidence trail behind every recommendation</li>
              <li>Degrading safely to hold when a signal is missing</li>
              <li>The blast-radius readout that must precede a revoke recommendation</li>
            </ul>
          </div>
          <div className="col col--6">
            <Heading as="h2" className={styles.ownsHeading}>
              What it refuses to own
            </Heading>
            <ul className={styles.ownsList}>
              <li>
                Any aggregate, invariant, or persisted domain state — see{' '}
                <Link to="/docs/ddd/subdomain-classification">
                  Subdomain classification
                </Link>
              </li>
              <li>
                Executing a recommendation — that stays a human decision (or
                a later, separately-gated act slice)
              </li>
              <li>Any write into a bounded context except via that context's own published tool</li>
              <li>Any Go-level dependency on the five contexts' internal packages</li>
            </ul>
          </div>
        </div>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={siteConfig.title}
      description="The read-side decision-support agent that correlates the fleet's five bounded contexts into one ranked, human-gated recommendation.">
      <HomepageHeader />
      <main>
        <HomepageFeatures />
        <WhatItOwns />
      </main>
    </Layout>
  );
}

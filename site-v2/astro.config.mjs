// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://zarldev.github.io',
  base: '/zarlmono',
  trailingSlash: 'always',
  // Share the real product recordings; V1 remains their source of truth.
  publicDir: '../site/public',
  integrations: [
    starlight({
      title: 'zarlcode / docs',
      description: 'Use zarlcode. Build your own agent with zkit.',
      customCss: ['./src/styles/docs.css'],
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/zarldev/zarlmono' }],
      sidebar: [
        { label: '← Product home', link: '/zarlmono/' },
        {
          label: 'Use zarlcode',
          items: [
            { label: 'Overview', slug: 'zarlcode' },
            { label: 'Install & first run', slug: 'zarlcode-onboarding' },
            { label: 'Your first workflow', slug: 'zarlcode-workflow' },
            { label: 'Interface guide', slug: 'zarlcode-interface' },
            { label: 'Providers & credentials', slug: 'zarlcode-providers' },
            { label: 'Sessions & transcripts', slug: 'sessions-transcripts' },
            { label: 'Safety & workspace access', slug: 'zarlcode-safety' },
            { label: 'Automation & CLI', slug: 'zarlcode-automation' },
          ],
        },
        {
          label: 'Build with zkit',
          items: [
            { label: 'Meet the toolkit', link: '/zarlmono/toolkit/' },
            { label: 'Quickstart', slug: 'getting-started' },
            { label: 'Architecture', slug: 'architecture' },
            { label: 'Examples', slug: 'examples' },
          ],
        },
        {
          label: 'Reference',
          collapsed: true,
          items: [
            { label: 'Runner', slug: 'runner' },
            { label: 'Verified completion', slug: 'pursue' },
            { label: 'Shared infrastructure', slug: 'shared-infra' },
            { label: 'Sub-agent tasks', slug: 'spawn' },
            { label: 'Tool system', slug: 'tools' },
            { label: 'Code tools', slug: 'code-tools' },
            { label: 'Guardrails', slug: 'guardrails' },
            { label: 'Compaction', slug: 'compaction' },
            { label: 'Sandboxing', slug: 'sandboxing' },
            { label: 'LLM providers', slug: 'providers' },
            { label: 'Tool ecosystem', slug: 'tool-ecosystem' },
            { label: 'Foundation packages', slug: 'foundation' },
          ],
        },
        {
          label: 'Project',
          collapsed: true,
          items: [
            { label: 'Feature coverage', slug: 'feature-coverage' },
            { label: 'SWE-bench evaluation', slug: 'swebench-eval' },
          ],
        },
      ],
    }),
  ],
});

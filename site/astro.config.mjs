// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// Deployed to GitHub Pages at zarldev.github.io/zarlmono.
export default defineConfig({
	site: 'https://zarldev.github.io',
	base: '/zarlmono',
	integrations: [
		starlight({
			title: 'zarlmono',
			description:
				'A Go-native agent toolkit and local terminal coding agent: runner, tools, guardrails, compaction, durable SQLite sessions, and canonical transcripts.',
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/zarldev/zarlmono' },
			],
			head: [
				{
					tag: 'link',
					attrs: {
						rel: 'preconnect',
						href: 'https://fonts.googleapis.com',
					},
				},
				{
					tag: 'link',
					attrs: {
						rel: 'preconnect',
						href: 'https://fonts.gstatic.com',
						crossorigin: true,
					},
				},
				{
					tag: 'link',
					attrs: {
						rel: 'stylesheet',
						href: 'https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;700&display=swap',
					},
				},
			],
			customCss: ['./src/styles/custom.css'],
			sidebar: [
				{
					label: 'Use zarlcode',
					items: [
						{ label: 'Overview', slug: 'zarlcode' },
						{ label: 'First run and onboarding', slug: 'zarlcode-onboarding' },
						{ label: 'Your first workflow', slug: 'zarlcode-workflow' },
						{ label: 'Interface guide', slug: 'zarlcode-interface' },
						{ label: 'Providers and credentials', slug: 'zarlcode-providers' },
						{ label: 'Sessions and transcripts', slug: 'sessions-transcripts' },
						{ label: 'Safety and workspace access', slug: 'zarlcode-safety' },
						{ label: 'Automation and CLI', slug: 'zarlcode-automation' },
					],
				},
				{
					label: 'Build with zkit',
					items: [
						{ label: 'Getting started', slug: 'getting-started' },
						{ label: 'Architecture', slug: 'architecture' },
					],
				},
				{
					label: 'The agent loop',
					items: [
						{ label: 'Runner', slug: 'runner' },
						{ label: 'Verified completion', slug: 'pursue' },
						{ label: 'Shared infrastructure', slug: 'shared-infra' },
						{ label: 'Sub-agent tasks', slug: 'spawn' },
					],
				},
				{
					label: 'Tools and guardrails',
					items: [
						{ label: 'The tool system', slug: 'tools' },
						{ label: 'Code tools', slug: 'code-tools' },
						{ label: 'Guardrails', slug: 'guardrails' },
						{ label: 'Compaction', slug: 'compaction' },
						{ label: 'Sandboxing', slug: 'sandboxing' },
					],
				},
				{
					label: 'Providers and foundations',
					items: [
						{ label: 'LLM providers', slug: 'providers' },
						{ label: 'Tool ecosystem', slug: 'tool-ecosystem' },
						{ label: 'Foundation packages', slug: 'foundation' },
					],
				},
				{ label: 'Examples', slug: 'examples' },
				{ label: 'Feature coverage', slug: 'feature-coverage' },
				{ label: 'swebench-eval', slug: 'swebench-eval' },
			],
		}),
	],
});

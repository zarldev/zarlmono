// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// Deployed to GitHub Pages at zarldev.github.io/zarlmono.
export default defineConfig({
	site: 'https://zarldev.github.io',
	base: '/zarlmono',
	integrations: [
		starlight({
			title: 'zarlmono/zkit',
			description:
				'A Go toolkit for building AI agents: the runner loop, verified completion, tools, guardrails, and compaction.',
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
					label: 'Start here',
					items: [
						{ label: 'Getting started', slug: 'getting-started' },
						{ label: 'Architecture', slug: 'architecture' },
					],
				},
				{
					label: 'The loop',
					items: [
						{ label: 'Runner', slug: 'runner' },
						{ label: 'Verified completion', slug: 'pursue' },
					],
				},
				{
					label: 'Tools',
					items: [
						{ label: 'The tool system', slug: 'tools' },
						{ label: 'Code tools', slug: 'code-tools' },
					],
				},
				{
					label: 'Keeping it honest',
					items: [
						{ label: 'Guardrails', slug: 'guardrails' },
						{ label: 'Compaction', slug: 'compaction' },
					],
				},
				{
					label: 'Plumbing',
					items: [
						{ label: 'LLM providers', slug: 'providers' },
						{ label: 'Sub-agents', slug: 'spawn' },
						{ label: 'Foundation packages', slug: 'foundation' },
						{ label: 'Sandboxing', slug: 'sandboxing' },
						{ label: 'Tool ecosystem', slug: 'tool-ecosystem' },
					],
				},
				{ label: 'Examples', slug: 'examples' },
				{
					label: 'Built with zkit',
					items: [
						{ label: 'zarlcode', slug: 'zarlcode' },
						{ label: 'zarlcode interface', slug: 'zarlcode-interface' },
						{ label: 'zarlai', slug: 'zarlai' },
						{ label: 'swebench-eval', slug: 'swebench-eval' },
					],
				},
			],
		}),
	],
});

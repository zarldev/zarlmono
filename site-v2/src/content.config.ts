import { defineCollection } from 'astro:content';
import { glob } from 'astro/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

// Keep shared documentation in place; V2 adds its own entry point and guides.
// The V1 splash is replaced by src/pages/index.astro, not imported.
export const collections = {
  docs: defineCollection({
    loader: glob({
      base: '..',
      pattern: ['site/src/content/docs/**/*.{md,mdx}', '!site/src/content/docs/index.mdx', 'site-v2/src/content/docs/**/*.{md,mdx}'],
      generateId: ({ entry }) => entry.replace(/^site(?:-v2)?\/src\/content\/docs\//, '').replace(/\.mdx?$/, ''),
    }),
    schema: docsSchema(),
  }),
};

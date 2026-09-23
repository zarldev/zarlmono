export const base = `${import.meta.env.BASE_URL.replace(/\/$/, '')}/`;
export const path = (slug = '') => `${base}${slug}`;
export const github = 'https://github.com/zarldev/zarlmono';

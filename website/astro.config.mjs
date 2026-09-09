// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLlmsTxt from 'starlight-llms-txt';
import { satteri } from '@astrojs/markdown-satteri';
import { satteriBaseLinks } from './src/plugins/satteri-base-links.mjs';

// Deployed to https://shibukawa.github.io/popcornweb/ by .github/workflows/docs.yml.
// This is the only place the base path is declared.
const base = '/popcornweb';

export default defineConfig({
  site: 'https://shibukawa.github.io',
  base,
  redirects: {
    '/guides/backend/cookies': `${base}/guides/backend/sessions/#using-cookies-directly`,
    '/ja/guides/backend/cookies': `${base}/ja/guides/backend/sessions/#クッキーを直接使う`,
    '/start/architecture': `${base}/guides/architecture/project-structure/`,
    '/ja/start/architecture': `${base}/ja/guides/architecture/project-structure/`,
    '/guides/frontend/compression': `${base}/guides/backend/compression/`,
    '/ja/guides/frontend/compression': `${base}/ja/guides/backend/compression/`,
    // The platform overview moved to the head of the Architecture group and
    // took a name that says what the list is.
    '/guides/architecture/platform': `${base}/guides/architecture/supported-platforms/`,
    '/ja/guides/architecture/platform': `${base}/ja/guides/architecture/supported-platforms/`,
    '/guides/cross-layer/tracing': `${base}/guides/architecture/telemetry/#reading-a-request-trace`,
    '/ja/guides/cross-layer/tracing': `${base}/ja/guides/architecture/telemetry/#リクエストトレースを読む`,
    '/guides/backend/token-revocation': `${base}/guides/backend/authentication/#revoking-a-bearer-token`,
    '/ja/guides/backend/token-revocation': `${base}/ja/guides/backend/authentication/#bearer-トークンを失効させる`,
    // pw prepare was renamed to pw generate, which now names the command that
    // leaves a compilable tree; the narrower generation it displaced is the
    // --code-only flag on the same page.
    '/pw/project/prepare': `${base}/pw/project/generate/`,
    '/ja/pw/project/prepare': `${base}/ja/pw/project/generate/`,
    // Renaming the framework moved the page that argues for it. The key is the
    // slug it had under the old name, so a bulk rename must not touch it.
    '/start/why-popcorn-wave': `${base}/start/why-popcorn-web/`,
    '/ja/start/why-popcorn-wave': `${base}/ja/start/why-popcorn-web/`,
  },
  markdown: {
    // Lets content link with plain `/guides/testing/` instead of repeating `base`.
    processor: satteri({ hastPlugins: [satteriBaseLinks({ base })] }),
  },
  integrations: [
    starlight({
      title: 'Popcorn Web',
      description:
        'A small, TinyGo-oriented web application framework for Go, built directly on net/http.',
      logo: {
        src: './src/assets/logo.png',
        alt: 'Popcorn Web',
        replacesTitle: true,
      },
      // The same mark pw dev puts on its console launcher button, so the tab
      // and the button in the corner of a development page read as one thing.
      // It is a copy of pw/devmark.webp as PNG, because Starlight's favicon
      // option takes .ico, .gif, .jpg, .png, or .svg and not WebP.
      favicon: '/favicon.png',
      defaultLocale: 'root',
      locales: {
        root: { label: 'English', lang: 'en' },
        ja: { label: '日本語', lang: 'ja' },
      },
      social: [
        {
          icon: 'github',
          label: 'GitHub',
          href: 'https://github.com/shibukawa/popcornweb',
        },
      ],
      editLink: {
        baseUrl: 'https://github.com/shibukawa/popcornweb/edit/main/website/',
      },
      components: {
        // Applies `base` to hero action links, which come from frontmatter and
        // are therefore out of reach of the base-links Markdown plugin.
        Hero: './src/components/Hero.astro',
      },
      // Groups are declared once; pages appear by dropping a Markdown file into
      // the matching directory (and its ja/ counterpart). No config change needed.
      sidebar: [
        {
          label: 'Start Here',
          translations: { ja: 'はじめに' },
          items: [{ autogenerate: { directory: 'start' } }],
        },
        {
          label: 'Tutorial',
          translations: { ja: 'チュートリアル' },
          items: [{ autogenerate: { directory: 'tutorial' } }],
        },
        {
          label: 'Guides',
          translations: { ja: 'ガイド' },
          items: [
            {
              label: 'Architecture',
              translations: { ja: 'アーキテクチャ' },
              items: [{ autogenerate: { directory: 'guides/architecture' } }],
            },
            {
              label: 'For Frontend',
              translations: { ja: 'フロントエンド' },
              items: [
                { autogenerate: { directory: 'guides/frontend' } },
                {
                  label: 'Interactivity',
                  translations: { ja: 'インタラクション' },
                  items: [{ autogenerate: { directory: 'guides/interactivity' } }],
                },
              ],
            },
            {
              label: 'As a Backend',
              translations: { ja: 'バックエンド' },
              items: [{ autogenerate: { directory: 'guides/backend' } }],
            },
            {
              label: 'Storage',
              translations: { ja: 'ストレージ' },
              items: [{ autogenerate: { directory: 'guides/storage' } }],
            },
            {
              label: 'Cross-layer Features',
              translations: { ja: 'レイヤー横断機能' },
              items: [{ autogenerate: { directory: 'guides/cross-layer' } }],
            },
            {
              label: 'Deployment',
              translations: { ja: 'デプロイ' },
              items: [{ autogenerate: { directory: 'guides/deployment' } }],
            },
          ],
        },
        {
          label: 'Productivity Support',
          translations: { ja: '開発支援' },
          items: [{ autogenerate: { directory: 'productivity' } }],
        },
        {
          label: 'pw command',
          translations: { ja: 'pw コマンド' },
          items: [
            'pw/overview',
            {
              label: 'Project',
              translations: { ja: 'プロジェクト' },
              items: [{ autogenerate: { directory: 'pw/project' } }],
            },
            {
              label: 'Database',
              translations: { ja: 'データベース' },
              items: [{ autogenerate: { directory: 'pw/database' } }],
            },
          ],
        },
        {
          label: 'Reference',
          translations: { ja: 'リファレンス' },
          // Three groups rather than one autogenerated list: what the
          // application calls, the formats and structs it declares, and the
          // settings the framework itself reads. Listing the slugs is what
          // makes the split visible, so `sidebar.order` inside each page now
          // only orders it within its own group.
          items: [
            {
              label: 'API',
              translations: { ja: 'API' },
              items: ['reference/runtime'],
            },
            {
              label: 'Formats and Declarations',
              translations: { ja: 'フォーマットと定義' },
              items: [
                'reference/request-binding',
                'reference/template-syntax',
                'reference/sql-templates',
                'reference/dynamo-templates',
                'reference/firestore-templates',
                'reference/configuration-declaration',
              ],
            },
            {
              label: 'Framework Settings',
              translations: { ja: 'フレームワークの設定' },
              items: [
                'reference/configuration',
                'reference/build-configuration',
                'reference/build-tags',
              ],
            },
          ],
        },
        {
          label: 'Appendix',
          translations: { ja: '付録' },
          items: [{ autogenerate: { directory: 'appendix' } }],
        },
      ],
      // Publishes /llms.txt, /llms-full.txt and /llms-small.txt for LLM consumers.
      plugins: [starlightLlmsTxt()],
    }),
  ],
});

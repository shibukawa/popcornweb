#!/usr/bin/env node
// Mechanical checks for the Popcorn Web documentation site.
//
// Run from the repository root:
//   node .claude/skills/docs-quality/check_docs.mjs
//   node .claude/skills/docs-quality/check_docs.mjs --only=links,parity
//   node .claude/skills/docs-quality/check_docs.mjs --path=guides/frontend
//   node .claude/skills/docs-quality/check_docs.mjs --json
//
// Everything here is decidable by a machine. Judgement calls (prose density,
// whether a guide earns its third case) belong to SKILL.md, not to this file.

import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join, relative, dirname, basename, extname } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = process.env.PW_REPO_ROOT || join(HERE, '..', '..', '..');
const DOCS = join(ROOT, 'website', 'src', 'content', 'docs');
const ASTRO_CONFIG = join(ROOT, 'website', 'astro.config.mjs');

// ---------------------------------------------------------------------------
// Tunables. Extend these as the site grows; they are the only per-project state.
// ---------------------------------------------------------------------------

// Routes retired by past reorganisations. Detected from git history as well,
// but these stay listed so the check works in a shallow clone.
const REMOVED_ROUTES = {
  '/start/getting-started/': '/tutorial/getting-started/',
  '/guides/configuration/': '/guides/architecture/configuration/',
  '/guides/architecture/platform/': '/guides/architecture/supported-platforms/',
};

// Pages where the reader has no Tailwind build yet, so a code block must not
// show Tailwind utility classes. `until` stops the check at that heading slug
// (the point in the page where Tailwind gets installed); omit it to cover the
// whole page.
const TAILWIND_OFF = [
  { route: '/tutorial/getting-started/' },
  { route: '/ja/tutorial/getting-started/' },
];

// Chapters in order, for the code-continuity ledger.
const TUTORIAL_ORDER = ['getting-started', 'forms', 'database', 'login', 'page-tree'];

const BANNED_TERMS = [
  {
    id: 'routing-vocabulary',
    re: /\b(classic|modern|legacy)[ -](routing|router|mode|style)\b|\b(routing|router)\s+(is\s+)?(classic|modern|legacy)\b|(クラシック|モダン|レガシー)(な)?(ルーティング|ルータ)/gi,
    say: 'routing modes are `registered` and `discovered` — never classic / modern / legacy',
  },
  {
    id: 'servemux-wrapper',
    re: /pw\.ServeMux[^.\n]{0,80}(wrapper|wraps|ラッパー|包んで)/gi,
    say: '`pw.ServeMux` is a type alias for `net/http.ServeMux` on host Go, not a wrapper around it',
  },
  {
    id: 'template-engine',
    re: /\.pw\.(html|sql)[^.\n]{0,60}(template engine|templating engine|テンプレートエンジン)/gi,
    say: '`.pw.html` / `.pw.sql` are typed template / query languages compiled by `pw generate`, not runtime template engines',
  },
  {
    id: 'edit-generated',
    // The instruction reads either way round — "edit the generated foo_pw_gen.go"
    // and "written to foo_pw_gen.go; edit it if you need to" are both fatal.
    re: /(edit|modify|hand-edit|change)[^.。\n]{0,60}_pw_gen\.go|_pw_gen\.go[^.。\n]{0,60}\b(edit|modify|hand-edit)\b|(編集|書き換え)[^.。\n]{0,60}_pw_gen\.go|_pw_gen\.go[^.。\n]{0,60}(を編集|を書き換え)/gi,
    say: '`_pw_gen.go` files are build output — the docs must never tell a reader to edit one',
  },
  {
    id: 'difficulty-label',
    re: /^\s*(sidebar:)?\s*badge:\s*['"]?(advanced|basic|beginner|上級|初級)['"]?/gim,
    say: 'the site has no difficulty labels — do not introduce advanced / basic badges',
  },
];

// Tailwind-ish utility token. Variants (`sm:`, `hover:`, `dark:`) are stripped
// before the test.
const TAILWIND_TOKEN =
  /^(text|bg|border|rounded|shadow|font|leading|tracking|p|px|py|pt|pb|pl|pr|m|mx|my|mt|mb|ml|mr|w|h|min-w|max-w|min-h|max-h|flex|grid|gap|space-x|space-y|items|justify|self|order|col|row|opacity|z|inset|top|right|bottom|left|overflow|cursor|ring|divide|outline|transition|duration|ease|scale|rotate|translate|backdrop|placeholder|accent|aspect|object|list|whitespace|break|align|table|float|clear|isolate|mix)-[a-z0-9[\]./-]+$|^(block|inline|inline-block|inline-flex|flex|grid|hidden|contents|relative|absolute|fixed|sticky|static|container|truncate|antialiased|italic|underline|uppercase|lowercase|capitalize|sr-only|not-sr-only)$/;

const SEVERITY_ORDER = { error: 0, warn: 1, info: 2 };

// ---------------------------------------------------------------------------
// Argument handling
// ---------------------------------------------------------------------------

const args = process.argv.slice(2);
const argOf = (name) => {
  const hit = args.find((a) => a.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : null;
};
const asJson = args.includes('--json');
const only = argOf('only')?.split(',').map((s) => s.trim()).filter(Boolean) ?? null;
const pathFilter = argOf('path');
const wants = (check) => !only || only.includes(check);

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) out.push(...walk(full));
    else if (['.md', '.mdx'].includes(extname(name))) out.push(full);
  }
  return out;
}

function splitFrontmatter(raw) {
  if (!raw.startsWith('---')) return { fm: {}, body: raw, bodyOffset: 0, fmRaw: '' };
  const end = raw.indexOf('\n---', 3);
  if (end === -1) return { fm: {}, body: raw, bodyOffset: 0, fmRaw: '' };
  const fmRaw = raw.slice(4, end);
  const body = raw.slice(raw.indexOf('\n', end + 1) + 1);
  return { fm: parseSimpleYaml(fmRaw), body, bodyOffset: raw.slice(0, end).split('\n').length + 1, fmRaw };
}

// Enough YAML for Starlight frontmatter: scalars, one nesting level, sequences
// of scalars. No anchors, no flow maps beyond `{}`.
function parseSimpleYaml(text) {
  const root = {};
  const stack = [{ indent: -1, node: root }];
  for (const line of text.split('\n')) {
    if (!line.trim() || line.trim().startsWith('#')) continue;
    const indent = line.length - line.trimStart().length;
    while (stack.length > 1 && indent <= stack[stack.length - 1].indent) stack.pop();
    const parent = stack[stack.length - 1].node;
    const trimmed = line.trim();
    if (trimmed.startsWith('- ')) {
      const key = stack[stack.length - 1].lastKey;
      if (key) {
        if (!Array.isArray(parent[key])) parent[key] = [];
        parent[key].push(unquote(trimmed.slice(2)));
      }
      continue;
    }
    const m = trimmed.match(/^([\w.-]+):\s*(.*)$/);
    if (!m) continue;
    const [, key, rest] = m;
    if (rest === '') {
      const child = {};
      parent[key] = child;
      stack[stack.length - 1].lastKey = key;
      stack.push({ indent, node: child });
    } else {
      parent[key] = unquote(rest);
      stack[stack.length - 1].lastKey = key;
    }
  }
  return root;
}

const unquote = (s) => s.replace(/^['"]|['"]$/g, '').trim();

function routeOf(file) {
  let rel = relative(DOCS, file).replace(/\\/g, '/').replace(/\.mdx?$/, '');
  if (basename(rel) === 'index') rel = dirname(rel) === '.' ? '' : dirname(rel);
  return rel === '' ? '/' : `/${rel}/`;
}

// Fenced code, inline code and HTML comments are invisible to the prose checks.
function stripCode(body) {
  return body
    .replace(/^```[\s\S]*?^```/gm, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/`[^`\n]*`/g, (m) => ' '.repeat(m.length))
    .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '));
}

function fencedBlocks(body) {
  const blocks = [];
  const re = /^```([^\n]*)\n([\s\S]*?)^```/gm;
  let m;
  while ((m = re.exec(body)) !== null) {
    blocks.push({
      meta: m[1].trim(),
      code: m[2],
      line: body.slice(0, m.index).split('\n').length,
    });
  }
  return blocks;
}

// Mirrors github-slugger, which is what Starlight's heading ids come from.
// Two details matter and are easy to get wrong: punctuation is deleted rather
// than replaced, and each remaining space becomes its own dash — so a heading
// written `cookie — no storage at all` anchors as `cookie--no-storage-at-all`.
// A third detail decides the two emphasis markers, and they part company: `*` is
// removed and `_` is kept, everywhere and regardless of code spans. The library
// removes a punctuation set rather than parsing emphasis, and `_` is a word
// character that never made that set. So `**b** _i_` anchors as `b-_i_`, and a
// heading named for a key — `### \`_operation\`: what it does` — anchors as
// `_operation-what-it-does` with the underscore intact.
function slugify(text) {
  return text
    .replace(/`([^`]*)`/g, '$1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\p{M}_ -]/gu, '')
    .replace(/ /g, '-');
}

// Only fenced blocks are masked here. Inline code inside a heading is part of
// its text, and blanking it would invent anchors nobody wrote.
function headingsOf(body) {
  const seen = new Map();
  const out = [];
  const masked = body.replace(/^```[\s\S]*?^```/gm, (m) => m.replace(/[^\n]/g, ' '));
  for (const line of masked.split('\n')) {
    const m = line.match(/^(#{2,6})\s+(.*)$/);
    if (!m) continue;
    let s = slugify(m[2]);
    if (seen.has(s)) {
      const n = seen.get(s) + 1;
      seen.set(s, n);
      s = `${s}-${n}`;
    } else seen.set(s, 0);
    out.push({ text: m[2].trim(), slug: s, level: m[1].length });
  }
  return out;
}

const pages = walk(DOCS)
  .sort()
  .map((file) => {
    const raw = readFileSync(file, 'utf8');
    const { fm, body, fmRaw } = splitFrontmatter(raw);
    const rel = relative(ROOT, file).replace(/\\/g, '/');
    return {
      file,
      rel,
      raw,
      fm,
      fmRaw,
      body,
      route: routeOf(file),
      locale: routeOf(file).startsWith('/ja/') ? 'ja' : 'en',
      headings: headingsOf(body),
    };
  });

const byRoute = new Map(pages.map((p) => [p.route, p]));
const selected = pathFilter
  ? pages.filter((p) => p.rel.includes(pathFilter) || p.route.includes(pathFilter))
  : pages;

// ---------------------------------------------------------------------------
// Findings
// ---------------------------------------------------------------------------

const findings = [];
const report = (check, severity, page, line, message, hint) =>
  findings.push({ check, severity, file: page.rel, line, message, hint });

// --- links -----------------------------------------------------------------

function removedRoutesFromGit() {
  try {
    const out = execFileSync(
      'git',
      ['log', '--all', '--diff-filter=D', '--name-only', '--pretty=format:', '--', 'website/src/content/docs'],
      { cwd: ROOT, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] },
    );
    const routes = new Set();
    for (const line of out.split('\n')) {
      const p = line.trim();
      if (!p.startsWith('website/src/content/docs/')) continue;
      const stripped = p.replace('website/src/content/docs/', '').replace(/\.mdx?$/, '');
      routes.add(`/${stripped}/`);
    }
    return routes;
  } catch {
    return new Set();
  }
}

if (wants('links')) {
  const gitRemoved = removedRoutesFromGit();
  const linkRe = /\[[^\]]*\]\((\/[^)\s]*)\)/g;

  for (const page of selected) {
    const masked = stripCode(page.body);
    let m;
    while ((m = linkRe.exec(masked)) !== null) {
      const href = m[1];
      if (href.startsWith('//')) continue;
      const line = masked.slice(0, m.index).split('\n').length;
      const [pathPart, anchor] = href.split('#');
      const target = pathPart === '' ? page.route : pathPart;

      if (!byRoute.has(target)) {
        const replacement = REMOVED_ROUTES[target];
        if (replacement) {
          report('links', 'error', page, line, `link to retired route \`${target}\``, `the reorganisation moved it — link \`${replacement}\``);
        } else if (gitRemoved.has(target) || gitRemoved.has(target.replace(/\/$/, ''))) {
          report('links', 'error', page, line, `link to \`${target}\`, a page deleted in an earlier commit`, 'find where the content moved and link that');
        } else {
          report('links', 'error', page, line, `broken internal link \`${target}\``, 'no page produces this route');
        }
        continue;
      }

      if (anchor) {
        const dest = byRoute.get(target);
        if (!dest.headings.some((h) => h.slug === anchor)) {
          const near = dest.headings.map((h) => h.slug).filter((s) => s.includes(anchor.slice(0, 6))).slice(0, 3);
          report('links', 'error', page, line, `\`${target}#${anchor}\` has no such heading`, near.length ? `did you mean ${near.map((s) => `#${s}`).join(', ')}?` : `${dest.rel} has no matching heading`);
        }
      }

      // A Japanese page linking into the English tree drops the reader out of
      // their locale, provided the Japanese page exists.
      if (page.locale === 'ja' && !target.startsWith('/ja/') && byRoute.has(`/ja${target}`)) {
        report('links', 'warn', page, line, `links to the English \`${target}\``, `use \`/ja${target}\``);
      }
      if (page.locale === 'en' && target.startsWith('/ja/')) {
        report('links', 'warn', page, line, `an English page links into \`${target}\``, `use \`${target.replace('/ja', '')}\``);
      }
    }
  }
}

// --- frontmatter -----------------------------------------------------------

if (wants('frontmatter')) {
  for (const page of selected) {
    if (!page.fm.title) report('frontmatter', 'error', page, 1, 'no `title`', 'Starlight needs it for the sidebar and the tab');
    const isHero = page.fm.template === 'splash' || page.fm.hero;
    if (!page.fm.description) {
      report('frontmatter', 'error', page, 1, 'no `description`', 'it is the search snippet and the social card — one sentence saying what the page settles');
    } else if (!isHero) {
      // Japanese says the same thing in roughly half the characters, so the
      // floor is per-locale. These bounds only catch a description that never
      // became a sentence, or one that became a paragraph.
      const d = String(page.fm.description);
      const floor = page.locale === 'ja' ? 12 : 28;
      const ceiling = page.locale === 'ja' ? 120 : 200;
      if (d.length < floor) report('frontmatter', 'warn', page, 1, `description is ${d.length} characters`, 'too short to say what the page settles');
      if (d.length > ceiling) report('frontmatter', 'warn', page, 1, `description is ${d.length} characters`, 'trim to one sentence — search results cut it off');
    }
    if (!isHero && page.fm.sidebar?.order === undefined && !page.route.endsWith('/overview/')) {
      report('frontmatter', 'warn', page, 1, 'no `sidebar.order`', 'the group falls back to alphabetical order, which rarely matches reading order');
    }
    const jaCounterpart = page.locale === 'en' && byRoute.get(`/ja${page.route}`);
    if (jaCounterpart && page.fm.sidebar?.order !== jaCounterpart.fm.sidebar?.order) {
      report('frontmatter', 'warn', page, 1, `sidebar.order ${page.fm.sidebar?.order} but the Japanese page says ${jaCounterpart.fm.sidebar?.order}`, 'the two sidebars would list the group in different orders');
    }
  }
}

// --- locale parity ---------------------------------------------------------

if (wants('parity')) {
  // Reporting honours --path; resolution never does, so a scoped run still
  // knows about every page on the site.
  for (const page of selected) {
    if (page.locale !== 'en') continue;
    const jaRoute = page.route === '/' ? '/ja/' : `/ja${page.route}`;
    if (!byRoute.has(jaRoute)) {
      report('parity', 'error', page, 1, `no Japanese counterpart (${jaRoute})`, `write website/src/content/docs/ja/${relative(DOCS, page.file).replace(/\\/g, '/')}`);
    }
  }
  for (const page of selected) {
    if (page.locale !== 'ja') continue;
    const enRoute = page.route === '/ja/' ? '/' : page.route.replace('/ja', '');
    if (!byRoute.has(enRoute)) {
      report('parity', 'error', page, 1, `no English counterpart (${enRoute})`, 'English is the default locale — it cannot be the one that is missing');
    }
  }
  // Heading structure drift makes the two locales impossible to review together.
  for (const page of selected) {
    if (page.locale !== 'en') continue;
    const ja = byRoute.get(page.route === '/' ? '/ja/' : `/ja${page.route}`);
    if (!ja) continue;
    const en2 = page.headings.filter((h) => h.level === 2).length;
    const ja2 = ja.headings.filter((h) => h.level === 2).length;
    if (en2 !== ja2) {
      report('parity', 'warn', page, 1, `${en2} top-level sections here, ${ja2} in ${ja.rel}`, 'the translations have drifted apart structurally');
    }
  }
}

// --- Tailwind in a Tailwind-free context -----------------------------------

if (wants('tailwind')) {
  for (const rule of TAILWIND_OFF) {
    const page = byRoute.get(rule.route);
    if (!page) continue;
    if (pathFilter && !selected.includes(page)) continue;
    let scope = page.body;
    if (rule.until) {
      const idx = page.body.split('\n').findIndex((l) => l.startsWith('#') && slugify(l.replace(/^#+\s*/, '')) === rule.until);
      if (idx > 0) scope = page.body.split('\n').slice(0, idx).join('\n');
    }
    for (const block of fencedBlocks(scope)) {
      const classes = [...block.code.matchAll(/class(?:Name)?=["']([^"']+)["']/g)];
      for (const c of classes) {
        const hits = c[1]
          .split(/\s+/)
          .map((t) => t.split(':').pop())
          .filter((t) => TAILWIND_TOKEN.test(t));
        if (hits.length) {
          report(
            'tailwind',
            'error',
            page,
            block.line,
            `code block uses Tailwind utilities (${hits.slice(0, 4).join(' ')}) where the reader has no Tailwind build`,
            'this page is listed in TAILWIND_OFF — use the classes `pw init` writes into public/app.css, or move the example after Tailwind is installed',
          );
        }
      }
    }
  }
}

// --- tutorial code continuity ----------------------------------------------

if (wants('tutorial')) {
  const SYMBOL = /^\s*(?:export\s+component\s+(\w+)|func\s+(?:\([^)]*\)\s*)?(\w+)|type\s+(\w+)\s+struct)/gm;
  for (const locale of ['en', 'ja']) {
    const prefix = locale === 'ja' ? '/ja/tutorial/' : '/tutorial/';
    const ledger = new Map(); // file path shown in a block -> { chapter, symbols }
    for (const chapter of TUTORIAL_ORDER) {
      const page = byRoute.get(`${prefix}${chapter}/`);
      if (!page) continue;
      for (const block of fencedBlocks(page.body)) {
        const first = block.code.split('\n').find((l) => l.trim());
        const pathComment = first?.match(/^\s*(?:\/\/|#|--)\s*([\w./-]+\.(?:go|pw\.html|pw\.sql|toml|css|json))\s*$/);
        if (!pathComment) continue;
        const shown = pathComment[1];
        const full = /^\s*package\s+\w+/m.test(block.code);
        const symbols = new Set();
        let s;
        SYMBOL.lastIndex = 0;
        while ((s = SYMBOL.exec(block.code)) !== null) symbols.add(s[1] || s[2] || s[3]);

        const prior = ledger.get(shown);
        if (prior && full && prior.full) {
          // A removal the chapter names is a removal the reader can follow. The
          // block's own comments count, and so does the paragraph under it.
          const nearby = page.body.split('\n').slice(block.line, block.line + block.code.split('\n').length + 12).join('\n');
          const gone = [...prior.symbols].filter(
            (sym) => !symbols.has(sym) && !block.code.includes(sym) && !nearby.includes(sym),
          );
          if (gone.length) {
            report(
              'tutorial',
              'info',
              page,
              block.line,
              `\`${shown}\` no longer shows ${gone.map((g) => `\`${g}\``).join(', ')}, introduced in ${prior.chapter}`,
              'intentional when the chapter says it removed them — otherwise the reader who followed along has code this block contradicts',
            );
          }
        }
        ledger.set(shown, {
          chapter,
          full,
          symbols: prior && !full ? new Set([...prior.symbols, ...symbols]) : symbols,
        });
      }
    }
  }
}

// --- sidebar versus filesystem ---------------------------------------------

if (wants('sidebar')) {
  const config = existsSync(ASTRO_CONFIG) ? readFileSync(ASTRO_CONFIG, 'utf8') : '';
  const autoDirs = [...config.matchAll(/autogenerate:\s*{\s*directory:\s*['"]([^'"]+)['"]/g)].map((m) => m[1]);
  // Every bare string in an array position, which is what an explicit sidebar
  // entry looks like. The lookbehind is what separates a slug from a value with
  // a key in front of it — `label:`, `directory:`, `translations:` — and the
  // leading-slash filter drops the redirect map's keys, which sit in an object
  // and are also preceded by a comma. Matching only the first string of each
  // array would report every later page of a hand-listed group as unreachable.
  const explicit = [...config.matchAll(/(?<=[[,]\s*)'([^']+)'/g)]
    .map((m) => m[1])
    .filter((s) => !s.startsWith('/'));
  const anchor = pages[0] ?? { rel: 'website/astro.config.mjs' };
  const cfgPage = { rel: relative(ROOT, ASTRO_CONFIG).replace(/\\/g, '/') };

  const covered = (route) => {
    const rel = route.replace(/^\/(ja\/)?/, '').replace(/\/$/, '');
    if (rel === '') return true;
    return (
      autoDirs.some((d) => rel === d || rel.startsWith(`${d}/`)) ||
      explicit.includes(rel)
    );
  };

  for (const page of selected) {
    if (page.locale !== 'en') continue;
    if (!covered(page.route)) {
      report('sidebar', 'error', page, 1, `${page.route} is in no sidebar group`, 'add an autogenerate entry in website/astro.config.mjs, or the page is reachable only by link');
    }
  }
  for (const dir of autoDirs) {
    const abs = join(DOCS, dir);
    if (!existsSync(abs)) {
      findings.push({ check: 'sidebar', severity: 'error', file: cfgPage.rel, line: 1, message: `autogenerate directory \`${dir}\` does not exist`, hint: 'the group renders empty' });
    } else if (walk(abs).length === 0) {
      findings.push({ check: 'sidebar', severity: 'warn', file: cfgPage.rel, line: 1, message: `autogenerate directory \`${dir}\` holds no pages`, hint: 'the group renders empty' });
    }
    if (!existsSync(join(DOCS, 'ja', dir))) {
      findings.push({ check: 'sidebar', severity: 'warn', file: cfgPage.rel, line: 1, message: `\`${dir}\` has no ja/ directory`, hint: 'the Japanese sidebar shows an empty group' });
    }
  }
  for (const slug of explicit) {
    if (!byRoute.has(`/${slug}/`)) {
      findings.push({ check: 'sidebar', severity: 'error', file: cfgPage.rel, line: 1, message: `sidebar names \`${slug}\`, which has no file`, hint: 'the build fails on an unresolved sidebar entry' });
    }
  }
  void anchor;
}

// --- terminology -----------------------------------------------------------

if (wants('terms')) {
  for (const page of selected) {
    const text = page.fmRaw + '\n' + page.body;
    for (const term of BANNED_TERMS) {
      term.re.lastIndex = 0;
      let m;
      while ((m = term.re.exec(text)) !== null) {
        const line = text.slice(0, m.index).split('\n').length;
        report('terms', 'error', page, line, `"${m[0].trim().replace(/\s+/g, ' ')}"`, term.say);
      }
    }
  }
}

// --- prose shape (advisory input to an audit, never a verdict) -------------

if (wants('shape')) {
  for (const page of selected) {
    if (page.fm.template === 'splash') continue;
    const lines = stripCode(page.body).split('\n');
    const total = lines.filter((l) => l.trim()).length;
    if (total < 10) continue;
    const listed = lines.filter((l) => /^\s*([-*+]\s|\d+\.\s|\|)/.test(l)).length;
    const share = listed / total;
    const isGuide = /\/guides\/|\/tutorial\//.test(page.route);
    if (isGuide && share > 0.45) {
      report('shape', 'warn', page, 1, `${Math.round(share * 100)}% of the prose lines are bullets or table rows`, 'guides carry their reasoning in sentences — check whether any of this dropped a "because"');
    }
    // A guide is supposed to say where it stops. This looks for the language
    // that carries such a statement rather than for a heading, because the
    // sentence is often folded into an opening paragraph — which is fine. A hit
    // here is a question for the auditor, never a verdict: read the page.
    if (isGuide && !/\/tutorial\//.test(page.route)) {
      const limits =
        /when not to|do not (reach|use)|is not the right|does not replace|is not for|instead of (this|it)|leave it off|off by default|no reason to|only when|not worth|what is not here|使わない|使うべきでは|向いて(い)?ない|適していません|この場合は|不要です|代わりに/i;
      if (!limits.test(page.body)) {
        report('shape', 'info', page, 1, 'nothing in this page says when *not* to reach for the feature', 'every guide states its own boundary — check whether the page has one and this check simply missed the wording');
      }
    }
  }
}

// ---------------------------------------------------------------------------
// Output
// ---------------------------------------------------------------------------

findings.sort(
  (a, b) =>
    SEVERITY_ORDER[a.severity] - SEVERITY_ORDER[b.severity] ||
    a.check.localeCompare(b.check) ||
    a.file.localeCompare(b.file) ||
    a.line - b.line,
);

if (asJson) {
  console.log(JSON.stringify({ pages: pages.length, findings }, null, 2));
} else {
  const counts = { error: 0, warn: 0, info: 0 };
  let lastGroup = '';
  for (const f of findings) {
    counts[f.severity]++;
    const group = `${f.severity}/${f.check}`;
    if (group !== lastGroup) {
      console.log(`\n── ${f.severity.toUpperCase()}  ${f.check} ──`);
      lastGroup = group;
    }
    console.log(`${f.file}:${f.line}  ${f.message}`);
    if (f.hint) console.log(`    ↳ ${f.hint}`);
  }
  console.log(
    `\n${pages.length} pages · ${counts.error} error · ${counts.warn} warn · ${counts.info} info`,
  );
}

process.exit(findings.some((f) => f.severity === 'error') ? 1 : 0);

// Conventional Commits, with a few project-specific tweaks.
// Enforced on commit-msg via lefthook + in CI on PR titles.

/** @type {import('@commitlint/types').UserConfig} */
export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'type-enum': [
      2,
      'always',
      [
        'feat', // user-facing feature work
        'fix', // bug fix
        'perf', // performance improvement
        'refactor', // code change that neither fixes a bug nor adds a feature
        'docs', // documentation only
        'test', // adding/fixing tests
        'build', // build system, deps
        'ci', // CI configuration
        'chore', // tooling/repo housekeeping
        'style', // formatting only (no code change)
        'revert', // a revert commit
        'rank', // ranking-config-only changes (tuning weights / formulas)
        'bench', // bench-only changes
        'data', // ingestion / migration / data-shape changes
      ],
    ],
    'scope-enum': [
      2,
      'always',
      [
        'api',
        'web',
        'ingest',
        'schema',
        'config',
        'rank',
        'intent',
        'retrieve',
        'observ',
        'bench',
        'ci',
        'hooks',
        'docs',
        'deps',
        'repo',
      ],
    ],
    'scope-empty': [2, 'never'],
    // Disallow Title/Sentence/ALL-CAPS subjects, but allow acronyms (CI, API,
    // SQL, JSON) inside an otherwise lower-case subject.
    'subject-case': [2, 'never', ['sentence-case', 'start-case', 'pascal-case', 'upper-case']],
    'subject-empty': [2, 'never'],
    'subject-full-stop': [2, 'never', '.'],
    'header-max-length': [2, 'always', 100],
    'body-leading-blank': [2, 'always'],
    'footer-leading-blank': [2, 'always'],
  },
  helpUrl:
    'https://www.conventionalcommits.org/ — type(scope): subject  (e.g. "feat(rank): hard-pin exact-name path")',
};

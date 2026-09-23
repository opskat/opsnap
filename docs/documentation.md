# Documentation maintenance

Find tracked docs with `git ls-files '*.md'`. Check facts with git-aware commands; untracked working files are not evidence of committed state.

## Language

Contributor docs (`AGENTS.md`, `docs/`, READMEs) are written in English. Specs under `docs/specs/` and verification reports under `e2e/scratch/` are written in Chinese.

## Ownership

| File | Owns |
|---|---|
| `AGENTS.md` | project facts, routing, hard rules, principles, definition of done, quick map |
| `docs/develop.md` | commands, layout, style, enforced rules, commits, CI |
| `docs/testing.md` | test design and commands |
| `docs/verification.md` | test environment and runtime verification workflow |
| `docs/design.md` | design system |
| `docs/architecture.md` | layering, subsystems, extension recipes, migrations |
| `docs/documentation.md` | this policy |
| `docs/README.md` | index |
| `e2e/README.md` | e2e harness and scratch scripts |

Keep each fact in its owner and link to it elsewhere; do not copy it.

## docs/specs/ is a record, not part of this set

Completed specs are not synced to the current code. During an active round, change a spec only when the requirement itself changes, get approval again, and commit that revision; never edit a spec to match an implementation. A superseding requirement gets a new spec, and the old one's status is updated.

Specs stay out of the index and the ownership table, but their links must resolve and they must not contain credentials or personal data.

## Fact and policy audit

For every changed claim, verify:

| Claim | Evidence |
|---|---|
| file or path | `git ls-files <path>` |
| identifier or signature | `git grep` plus reading the file |
| count or list | enumerate the authoritative source now |
| lint scope | the configuration, including overrides and ignores |
| command | exists in the `Makefile` or `package.json` and was actually run |

For every rule you touch, be able to name its owner, trigger, action, exception or fallback, compliance evidence and stop condition. Treat absolute wording as a review queue:

```bash
git grep -n -Ei 'always|never|must|all ' -- AGENTS.md 'docs/*.md'
```

## Structural audit

- When adding, renaming or deleting a doc, update the index, the ownership table and the `AGENTS.md` routes.
- Every relative link and anchor must resolve.
- Label facts that exist only on a branch; do not present them as the state of `main`.
- After a rebase or conflict resolution, rerun the checks on the final tree.

```bash
git ls-files '*.md' | while IFS= read -r doc; do
  sed '/^```/,/^```/d' "$doc" | sed -E 's/`[^`]*`//g' \
    | grep -oE '\]\(([^)]+)\)' | sed -E 's/^\]\(|\)$//g' \
    | grep -vE '^(https?:|mailto:|#)' | while IFS= read -r link; do
      target="$(dirname "$doc")/${link%%#*}"
      [ -e "$target" ] || echo "broken $doc -> $link"
  done
done
```

## Deletion

Search tracked files for the removed identifier and clear links, index rows, lists, examples, comments, config, CI and helpers that are no longer used:

```bash
git grep -inw '<removed identifier>' -- .
```

Then rerun the link, anchor and policy checks and `make verify`, and report only the scope you actually checked.

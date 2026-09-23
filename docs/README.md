# Documentation index

Read [`../AGENTS.md`](../AGENTS.md) first: it owns routing and the project's non-negotiable rules.

| Document | Owns |
|---|---|
| [`develop.md`](develop.md) | commands, layout, style, enforced rules, commits, CI |
| [`architecture.md`](architecture.md) | layering, subsystems, extension recipes, migrations, generated output |
| [`testing.md`](testing.md) | test boundaries, coverage, mocks and fixtures, commands |
| [`verification.md`](verification.md) | docker.local test environment, runtime verification workflow and reports |
| [`design.md`](design.md) | tokens, type scale, components, themes, states, accessibility |
| [`documentation.md`](documentation.md) | documentation ownership and fact checks |
| [`references/verification-report-template.md`](references/verification-report-template.md) | verification report structure, verdicts and evidence (Chinese) |
| [`../e2e/README.md`](../e2e/README.md) | e2e smoke tests and scratch scripts |

`specs/` holds each round's requirements. They record what was agreed at the time and are not synced to the code; changing requirements during an active round needs renewed approval, as described in [`documentation.md`](documentation.md#docsspecs-is-a-record-not-part-of-this-set).

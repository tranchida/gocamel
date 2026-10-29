---
name: release
description: Prepare a GoCamel release - bump module dependencies, update CHANGELOG and READMEs, run the full check
disable-model-invocation: true
argument-hint: <version, e.g. v0.19.0>
---

Prepare release `$ARGUMENTS`. Model it on commit `da62faa` (`chore(release): prepare v0.18.0`). Do NOT create tags or push unless the user says so explicitly.

1. **Preconditions**: working tree is clean (`git status`), branch is `main`, and `./check.sh --lint` passes.
2. **Changelog**: collect commits since the last tag (`git log $(git describe --tags --abbrev=0)..HEAD --oneline`) and add a dated `$ARGUMENTS` section to `CHANGELOG.md`, grouped by Conventional Commit type, in the existing style.
3. **Docs**: update version references in `README.md`, `README.fr.md` and the Hugo pages under `hugo-site/content/en/` and `hugo-site/content/fr/` (search for the previous version string).
4. **Modules**: in every `components/*`, `observability/*` and `examples` module, bump the `github.com/tranchida/gocamel` requirement to `$ARGUMENTS` in `go.mod`. Because the tag does not exist yet, keep the `go.work` workspace in effect, and update `go.sum` only via the toolchain (`go mod tidy` once the tag is published), never by hand.
5. **Verify**: run `./check.sh` and report any failing module.
6. **Commit**: propose `chore(release): prepare $ARGUMENTS` and ask before committing. Tagging and pushing stay with the user.

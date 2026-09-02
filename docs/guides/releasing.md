# Releasing nvoken clients

SDK and CLI releases are built from the same reviewed commit but use separate
tags so a registry-specific failure can be retried independently.

## One-time registry setup

Create the GitHub environments `npm`, `pypi`, and `crates-io`, then configure
the corresponding trusted publishers:

- npm: organization `deepnoodle-ai`, repository `nvoken`, workflow
  `release-npm.yml`, environment `npm`, allowed action `npm publish`;
- PyPI: project `nvoken`, owner `deepnoodle-ai`, repository `nvoken`, workflow
  `release-pypi.yml`, environment `pypi` as its trusted publisher;
- crates.io: publish the first crate version with a scoped crates.io token,
  then configure repository `deepnoodle-ai/nvoken`, workflow
  `release-crates.yml`, environment `crates-io` as its trusted publisher.

Add `TAP_GITHUB_TOKEN` with write access to `deepnoodle-ai/homebrew-tap` as a
repository Actions secret.

## Prepare a release

1. Read the review threads on every pull request merged since the last release
   tag, and confirm anything a reviewer made approval conditional on was
   actually applied before that pull request merged. A conditional approval is
   not a merge gate, so the fix can be absent from the merged commit while the
   review still reads as satisfied.
2. Update `CHANGELOG.md` and the versions in `sdk/go/version.go`,
   `sdk/typescript/package.json`, `sdk/typescript/src/version.ts`,
   `sdk/python/pyproject.toml`, and `sdk/rust/Cargo.toml`.
3. Bump the `nvoken` entry in `sdk/rust/Cargo.lock` to match, with
   `cargo update -p nvoken --offline` from `sdk/rust`. The lockfile is tracked
   and pins the crate's own version, so `cargo --locked` fails to resolve
   without it.
4. Run `make sdk-generate` so generated package metadata uses those versions.
5. Run `make check`.
6. Merge the release pull request and confirm the `check` workflow passed on
   `main`.

Step 1 is the only place a release-workflow defect is visible before it fires.
`make check` runs on a branch checkout and cannot exercise the release
workflows, which run only on a tag push; 0.35.0's npm job aborted on a bug that
review had found, described, and given the fix for.

The OpenAPI documents keep their independent contract version. Do not change
`info.version` merely to match an SDK release.

## Publish an aligned version

Create annotated tags on the same verified `main` commit, then push them one at
a time. GitHub does not create tag-push workflow events when more than three
tags are pushed together.

```bash
version=0.10.0
git tag -a "v${version}" -m "nvoken CLI ${version}"
git tag -a "sdk/go/v${version}" -m "nvoken Go SDK ${version}"
git tag -a "npm-v${version}" -m "nvoken TypeScript SDK ${version}"
git tag -a "pypi-v${version}" -m "nvoken Python SDK ${version}"
git tag -a "crates-v${version}" -m "nvoken Rust SDK ${version}"
git push origin "v${version}"
git push origin "sdk/go/v${version}"
git push origin "npm-v${version}"
git push origin "pypi-v${version}"
git push origin "crates-v${version}"
```

If an npm tag workflow fails before the registry artifact exists, fix the
workflow on `main`, verify the version is still absent from npm, and recreate
only that unpublished npm tag on the fixed commit. npm trusted publishing must
run from a tag-push event; manual workflow dispatch can present a different
workflow identity to the registry. Never move or recreate a tag after its
artifact exists.

Verify every workflow, the registry artifacts, the GitHub release archives,
and the updated `deepnoodle-ai/homebrew-tap` formula before announcing the
release.

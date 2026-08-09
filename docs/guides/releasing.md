# Releasing nvoken clients

SDK and CLI releases are built from the same reviewed commit but use separate
tags so a registry-specific failure can be retried independently.

## One-time registry setup

Create the GitHub environments `npm`, `pypi`, and `crates-io`, then configure
the corresponding trusted publishers:

- npm: organization `deepnoodle-ai`, repository `nvoken`, workflow
  `release-npm.yml`, environment `npm`, allowed action `npm publish`;
- PyPI pending publisher: project `nvoken`, owner `deepnoodle-ai`, repository
  `nvoken`, workflow `release-pypi.yml`, environment `pypi`;
- crates.io: publish the first crate version with a scoped crates.io token,
  then configure repository `deepnoodle-ai/nvoken`, workflow
  `release-crates.yml`, environment `crates-io` as its trusted publisher.

Add `TAP_GITHUB_TOKEN` with write access to `deepnoodle-ai/homebrew-tap` as a
repository Actions secret.

## Prepare a release

1. Update `CHANGELOG.md` and the versions in `sdk/go/version.go`,
   `sdk/typescript/package.json`, `sdk/typescript/src/version.ts`,
   `sdk/python/pyproject.toml`, and `sdk/rust/Cargo.toml`.
2. Run `make sdk-generate` so generated package metadata uses those versions.
3. Run `make check` and `make openapi-sync-check
   NVOKEN_CLOUD_REPO=../nvoken-cloud`.
4. Merge the release pull request and confirm the `check` workflow passed on
   `main`.

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

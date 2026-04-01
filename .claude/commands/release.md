Create a new release for this project.

## Current state

Latest tag: !`git describe --tags --abbrev=0 2>/dev/null || echo "(none)"`

Commits since last tag:
!`git log $(git describe --tags --abbrev=0 2>/dev/null)..HEAD --oneline 2>/dev/null || git log --oneline`

## Instructions

1. Show the user the current version (from the latest git tag above) and ask them for the new version number. Wait for their response before continuing.

2. Validate the version:
   - Must be valid semver (e.g. `1.2.0`). Strip a leading `v` if provided.
   - The tag `v<version>` must not already exist.

3. Generate a changelog entry from the commits listed above:
   - Filter out commits prefixed with `docs:`, `test:`, `chore:`, or `ci:` (matching the .goreleaser.yaml changelog config).
   - Strip the short commit hash from each line.
   - Format as a `## v<version>` section with `- ` bullet points matching the style in CHANGELOG.md.

4. Prepend the new section to CHANGELOG.md, inserting it after the `# Changelog` header and before the first existing `## v` section.

5. Show the user the generated changelog entry and ask if they want to make any edits. If they do, apply the edits. If not, continue.

6. Commit CHANGELOG.md with message `Release v<version>`, create an annotated git tag `v<version>`, and push both the commit and tag to origin. Remember to follow the git rules in CLAUDE.md — ask the user before committing and pushing.

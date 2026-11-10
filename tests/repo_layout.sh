#!/usr/bin/env bash
# Checks that the repository layout files of M0-2 exist with their approved content.
# PRODUCT.md holds local paths that CI runners cannot see, so only its text is checked.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
status=0

# expect_content <path> <expected bytes>: the file must exist and match byte for byte.
expect_content() {
  local path="$1" expected="$2"
  if [ ! -f "$path" ]; then
    echo "repo_layout: missing file: $path"
    status=1
    return
  fi
  if ! cmp -s "$path" <(printf '%s' "$expected"); then
    echo "repo_layout: content differs from the approved content: $path"
    status=1
    return
  fi
  echo "repo_layout: ok: $path"
}

# expect_line <path> <line>: the file must exist and contain this exact line.
expect_line() {
  local path="$1" line="$2"
  if [ ! -f "$path" ]; then
    echo "repo_layout: missing file: $path"
    status=1
    return
  fi
  if ! grep -Fxq -- "$line" "$path"; then
    echo "repo_layout: missing line in $path: $line"
    status=1
    return
  fi
  echo "repo_layout: ok: $path has: $line"
}

expect_content PRODUCT.md $'PRD path: C:/products/_factory/intuitik/PRD.md\nRules copy path: C:/products/_factory/FACTORY_RULES.md\n'
expect_content .github/CODEOWNERS $'* @theprdguy\n'
expect_content .gitignore $'.DS_Store\nThumbs.db\n*.swp\n.idea/\n.vscode/\n.env\n.env.*\ncmd/intuitik/dist/\nweb/node_modules/\n'

# A custom Gitleaks config replaces the default rules unless it extends them.
expect_line .gitleaks.toml '[extend]'
expect_line .gitleaks.toml 'useDefault = true'
expect_line .gitleaks.toml 'id = "claude-code-oauth-token"'
expect_line .gitleaks.toml 'keywords = ["sk-ant-oat01-"]'

if [ ! -f .github/workflows/ci.yml ]; then
  echo "repo_layout: missing file: .github/workflows/ci.yml"
  status=1
fi

exit "$status"

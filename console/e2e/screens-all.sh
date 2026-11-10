#!/usr/bin/env bash
# Captures every screen as each kind of caller (admin, no-role, deployer) and signed out,
# desktop 1440 and phone 390, light and dark, into console/e2e/shots (git-ignored).
#   console/e2e/screens-all.sh path/to/harness.json
set -u
cd "$(dirname "${BASH_SOURCE[0]}")/.."
h="${1:?usage: screens-all.sh harness.json}"
rm -rf e2e/shots
node e2e/screens.mjs --harness "$h" --variants 1440-light,1440-dark,390-light,390-dark
node e2e/screens.mjs --harness "$h" --as norole --variants 1440-light,1440-dark,390-light,390-dark
node e2e/screens.mjs --harness "$h" --as deployer --variants 1440-light,1440-dark,390-light,390-dark
node e2e/screens.mjs --harness "$h" --as anonymous --variants 1440-light,1440-dark,390-light,390-dark

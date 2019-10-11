#!/usr/bin/env bash

set -euo pipefail

git config --global user.email "$GITHUB_ACTOR@users.noreply.github.com"
git config --global user.name "$GITHUB_ACTOR"

echo "Github event path is $GITHUB_EVENT_PATH"

cat <<- EOF > "$HOME/.netrc"
machine github.com
login $GITHUB_ACTOR"
password $GITHUB_TOKEN
machine api.github.com
login $GITHUB_ACTOR
password $GITHUB_TOKEN
EOF
chmod 600 "$HOME/.netrc"

git add vendor

if ! git diff --quiet
then
    echo "Committing changes from syncdeps"
    # shellcheck disable=SC2046
    echo "github ref is $GITHUB_REF"
    PUSH_BRANCH=$(echo "$GITHUB_REF" | awk -F / '{ print $3 }')
    echo "Push branch is $PUSH_BRANCH"
    git checkout "$PUSH_BRANCH"
    git add WORKSPACE
    git commit -m "Committing changes from syncdeps (go mod vendor && gazelle)"
    git push origin "$PUSH_BRANCH"
else
    echo "No changes found to commit."
fi
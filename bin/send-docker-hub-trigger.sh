#!/bin/bash

# This script will trigger a multi-stage Docker build at the Docker hub repo referenced by $DOCKER_HUB_TRIGGER_URL.

if [[ "$TRAVIS_PULL_REQUEST" == 'false' ]]; then
  echo "Triggering Docker Hub build on branch @@BRANCH"
  curl -H "Content-Type: application/json" --data '{"source_type": "Branch", "source_name": "@@BRANCH"}' -X POST "$DOCKER_HUB_TRIGGER_URL"
else
  echo "This is a pull request build; not triggering Docker hub build"
fi

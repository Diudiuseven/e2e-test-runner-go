#!/bin/sh
if [ "$E2E_RUNNER_ATTEMPT" = "0" ]; then
  echo "transient failure" >&2
  exit 1
fi
echo "recovered on attempt $E2E_RUNNER_ATTEMPT"
exit 0

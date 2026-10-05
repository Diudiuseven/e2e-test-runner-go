#!/bin/sh
# fails on the first attempt, passes afterwards; state lives in the
# per-scenario state dir provided by the runner
count=0
[ -f "$SCENARIO_STATE_DIR/count" ] && count=$(cat "$SCENARIO_STATE_DIR/count")
count=$((count + 1))
echo "$count" > "$SCENARIO_STATE_DIR/count"
echo "attempt number $count"
[ "$count" -ge 2 ]

#!/bin/sh
# Spawn a grandchild that outlives the shell if process-group cleanup is broken.
( sleep 300 & echo $! > child.pid )
while true; do sleep 10; done

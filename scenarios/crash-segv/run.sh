#!/bin/sh
echo "crashing with SIGSEGV"
kill -SEGV $$

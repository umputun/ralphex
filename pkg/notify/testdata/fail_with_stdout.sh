#!/bin/sh
# exits with non-zero code and writes to both stdout and stderr
echo "stdout info" >&1
echo "stderr info" >&2
exit 1

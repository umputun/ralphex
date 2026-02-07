#!/bin/sh
# sleeps longer than typical test timeout, uses exec to replace shell with sleep
# so context cancellation kills the sleep process directly
exec sleep 30

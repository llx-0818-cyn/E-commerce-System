#!/bin/sh
set -eu

/app/migrate up
exec /app/api

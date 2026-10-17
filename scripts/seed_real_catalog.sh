#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
if [[ ! -f data/real-catalog/catalog.json ]]; then
  python3 scripts/collect_real_catalog.py
fi
mkdir -p .run/backups
backup_file=".run/backups/before-real-catalog-$(date +%Y%m%d-%H%M%S).sql"
docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump --single-transaction --no-tablespaces -uroot "$MYSQL_DATABASE"' > "$backup_file"
echo "Database backup: $backup_file"
docker compose stop backend worker
trap 'docker compose up -d --no-build backend worker' EXIT
python3 scripts/import_real_catalog.py --apply
docker compose exec -T redis sh -c 'export REDISCLI_AUTH="$REDIS_PASSWORD"; redis-cli --scan --pattern "v1:*" | while IFS= read -r catalog_cache_key; do redis-cli UNLINK "$catalog_cache_key" >/dev/null; done'
# Keep a running production frontend's ID-to-source manifest aligned with MySQL.
docker compose cp frontend/public/catalog/. frontend:/usr/share/nginx/html/catalog/
